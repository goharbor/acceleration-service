// Copyright Project Harbor Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package nydus

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"time"

	"fmt"

	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/images/converter"
	"github.com/containerd/nydus-snapshotter/pkg/backend"
	nydusify "github.com/containerd/nydus-snapshotter/pkg/converter"
	encryption "github.com/containerd/nydus-snapshotter/pkg/encryption"
	"github.com/containerd/platforms"
	"github.com/goharbor/acceleration-service/pkg/adapter/annotation"
	"github.com/goharbor/acceleration-service/pkg/cache"
	accelcontent "github.com/goharbor/acceleration-service/pkg/content"
	"github.com/goharbor/acceleration-service/pkg/driver/nydus/parser"
	nydusutils "github.com/goharbor/acceleration-service/pkg/driver/nydus/utils"
	"github.com/goharbor/acceleration-service/pkg/errdefs"
	"github.com/goharbor/acceleration-service/pkg/utils"
	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

const (
	// annotationSourceDigest indicates the source OCI image digest.
	annotationSourceDigest = "containerd.io/snapshot/nydus-source-digest"
	// annotationSourceReference indicates the source OCI image reference name.
	annotationSourceReference = "containerd.io/snapshot/nydus-source-reference"
	// annotationFsVersion indicates the fs version (rafs v5/v6) of nydus image.
	annotationFsVersion = "containerd.io/snapshot/nydus-fs-version"
	// annotationBuilderVersion indicates the nydus builder (nydus-image) version.
	annotationBuilderVersion = "containerd.io/snapshot/nydus-builder-version"
	// emptyTarGzipUnpackedDigest is the canonical sha256 digest of empty tar file (1024 NULL bytes).
	// Can be used as the diffID of an empty layer tar.gz layer.
	emptyTarGzipUnpackedDigest = digest.Digest("sha256:5f70bf18a086007016e948b04aed3b82103a36bea41755b6cddfaf10ace3c6ef")
)

var builderVersion string
var builderVersionOnce sync.Once

type CacheRef struct{}

type chunkDictInfo struct {
	BootstrapPath string
}

type Driver struct {
	workDir           string
	builderPath       string
	fsVersion         string
	compressor        string
	chunkDictRef      string
	mergeManifest     bool
	ociRef            bool
	docker2oci        bool
	alignedChunk      bool
	chunkSize         string
	batchSize         string
	prefetchPatterns  string
	backend           backend.Backend
	platformMC        platforms.MatchComparer
	encryptRecipients []string
	withReferrer      bool
}

func detectBuilderVersion(ctx context.Context, builder string) string {
	builderVersionOnce.Do(func() {
		cmd := exec.CommandContext(ctx, builder, "--version")
		output, err := cmd.Output()
		if err != nil {
			return
		}

		re := regexp.MustCompile(`Version:\s*(v.*)`)
		matches := re.FindSubmatch(output)
		if len(matches) > 1 {
			builderVersion = strings.TrimSpace(string(matches[1]))
		}
	})
	return builderVersion
}

func parseBool(v string) (bool, error) {
	parsed := false
	if v != "" {
		var err error
		parsed, err = strconv.ParseBool(v)
		if err != nil {
			return false, fmt.Errorf("invalid merge_manifest option")
		}
	}
	return parsed, nil
}

func New(cfg map[string]string, platformMC platforms.MatchComparer) (*Driver, error) {
	workDir := cfg["work_dir"]
	if workDir == "" {
		workDir = os.TempDir()
	}

	builderPath := cfg["builder"]
	if builderPath == "" {
		builderPath = "nydus-image"
	}

	chunkDictRef := cfg["chunk_dict_ref"]

	var err error
	var _backend backend.Backend
	backendType := cfg["backend_type"]
	backendConfig := cfg["backend_config"]
	backendForcePush, err := parseBool(cfg["backend_force_push"])
	if err != nil {
		return nil, errors.Wrap(err, "invalid backend_force_push option")
	}
	if backendType != "" && backendConfig != "" {
		_backend, err = backend.NewBackend(backendType, []byte(backendConfig), backendForcePush)
		if err != nil {
			return nil, errors.Wrap(err, "create blob backend")
		}
	}

	docker2oci, err := parseBool(cfg["docker2oci"])
	if err != nil {
		return nil, errors.Wrap(err, "invalid docker2oci option")
	}

	fsAlignChunk, err := parseBool(cfg["fs_align_chunk"])
	if err != nil {
		return nil, errors.Wrap(err, "invalid fs_align_chunk option")
	}

	fsChunkSize := cfg["fs_chunk_size"]
	BatchSize := cfg["batch_size"]
	prefetchPatterns := cfg["prefetch_patterns"]

	fsVersion := cfg["fs_version"]
	if fsVersion == "" {
		// For compatibility of older configuration.
		fsVersion = cfg["rafs_version"]
		if fsVersion == "" {
			fsVersion = "6"
		}
	}
	compressor := cfg["compressor"]
	if compressor == "" {
		// For compatibility of older configuration.
		compressor = cfg["rafs_compressor"]
	}

	mergeManifest, err := parseBool(cfg["merge_manifest"])
	if err != nil {
		return nil, errors.Wrap(err, "invalid merge_manifest option")
	}

	ociRef, err := parseBool(cfg["oci_ref"])
	if err != nil {
		return nil, errors.Wrap(err, "invalid oci_ref option")
	}

	withReferrer, err := parseBool(cfg["with_referrer"])
	if err != nil {
		return nil, errors.Wrap(err, "invalid with_referrer option")
	}

	encryptRecipients := []string{}
	if cfg["encrypt_recipients"] != "" {
		encryptRecipients = strings.Split(cfg["encrypt_recipients"], ",")
	}

	if ociRef {
		if fsVersion != "6" {
			logrus.Warn("forcibly using fs version 6 when oci_ref option enabled")
			fsVersion = "6"
		}
		if !docker2oci {
			// For nydus zran image, since its manifest requires the use
			// of annotations, we only support OCI-formatted manifest.
			docker2oci = true
		}
	}

	return &Driver{
		workDir:           workDir,
		builderPath:       builderPath,
		fsVersion:         fsVersion,
		compressor:        compressor,
		chunkDictRef:      chunkDictRef,
		mergeManifest:     mergeManifest,
		ociRef:            ociRef,
		docker2oci:        docker2oci,
		alignedChunk:      fsAlignChunk,
		chunkSize:         fsChunkSize,
		batchSize:         BatchSize,
		prefetchPatterns:  prefetchPatterns,
		backend:           _backend,
		platformMC:        platformMC,
		encryptRecipients: encryptRecipients,
		withReferrer:      withReferrer,
	}, nil
}

func (d *Driver) Name() string {
	return "nydus"
}

func (d *Driver) Version() string {
	return ""
}

func (d *Driver) Convert(ctx context.Context, provider accelcontent.Provider, sourceRef string) (*ocispec.Descriptor, error) {
	image, err := provider.Image(ctx, sourceRef)
	if err != nil {
		return nil, errors.Wrap(err, "get source image")
	}
	desc, err := d.convert(ctx, provider, *image, sourceRef)
	if err != nil {
		return nil, err
	}
	if d.mergeManifest {
		return d.makeManifestIndex(ctx, provider.ContentStore(), *image, *desc)
	}
	return desc, err
}

func (d *Driver) convert(ctx context.Context, provider accelcontent.Provider, source ocispec.Descriptor, sourceRef string) (*ocispec.Descriptor, error) {
	cs := provider.ContentStore()

	chunkDictPath := ""
	if d.chunkDictRef != "" {
		chunkDictInfo, err := d.getChunkDict(ctx, provider)
		if err != nil {
			return nil, errors.Wrap(err, "get chunk dict info")
		}
		chunkDictPath = chunkDictInfo.BootstrapPath
	}

	packOpt := nydusify.PackOption{
		WorkDir:          d.workDir,
		BuilderPath:      d.builderPath,
		FsVersion:        d.fsVersion,
		PrefetchPatterns: d.prefetchPatterns,
		ChunkDictPath:    chunkDictPath,
		Compressor:       d.compressor,
		Backend:          d.backend,
		OCIRef:           d.ociRef,
		AlignedChunk:     d.alignedChunk,
		ChunkSize:        d.chunkSize,
		BatchSize:        d.batchSize,
		Encrypt:          len(d.encryptRecipients) != 0,
	}

	var encrypter nydusify.Encrypter
	if len(d.encryptRecipients) > 0 {
		encrypter = func(ctx context.Context, cs content.Store, desc ocispec.Descriptor) (ocispec.Descriptor, error) {
			return encryption.EncryptNydusBootstrap(ctx, cs, desc, d.encryptRecipients)
		}
	}
	mergeOpt := nydusify.MergeOption{
		WorkDir:          packOpt.WorkDir,
		BuilderPath:      packOpt.BuilderPath,
		FsVersion:        packOpt.FsVersion,
		ChunkDictPath:    packOpt.ChunkDictPath,
		PrefetchPatterns: packOpt.PrefetchPatterns,
		Backend:          packOpt.Backend,
		OCI:              d.docker2oci,
		OCIRef:           packOpt.OCIRef,
		Encrypt:          encrypter,
		WithReferrer:     d.withReferrer,
		MergeManifest:    d.mergeManifest,
	}
	convertHookFunc := func(
		ctx context.Context, cs content.Store, orgDesc ocispec.Descriptor, newDesc *ocispec.Descriptor,
	) (*ocispec.Descriptor, error) {
		// Append the nydus bootstrap layer for image manifest.
		desc, err := nydusify.ConvertHookFunc(mergeOpt)(ctx, cs, orgDesc, newDesc)
		if err != nil {
			return nil, err
		}
		if !images.IsManifestType(desc.MediaType) {
			return desc, err
		}

		// Append the nydus related annotations for image manifest.
		appended := map[string]string{
			annotationSourceDigest:    string(orgDesc.Digest),
			annotationSourceReference: sourceRef,
			annotationFsVersion:       d.fsVersion,
		}
		if version := detectBuilderVersion(ctx, d.builderPath); version != "" {
			appended[annotationBuilderVersion] = version
		}
		desc, err = annotation.Append(ctx, cs, desc, appended)
		if err != nil {
			return nil, errors.Wrap(err, "append annotations")
		}
		return desc, err
	}
	convertHooks := converter.ConvertHooks{
		PostConvertHook: convertHookFunc,
	}
	convertFunc := func(ctx context.Context, cs content.Store, desc ocispec.Descriptor) (*ocispec.Descriptor, error) {
		target, err := nydusify.LayerConvertFunc(packOpt)(ctx, cs, desc)
		if err == nil && target != nil {
			cache.Set(ctx, desc, *target)
		}
		return target, err
	}
	indexConvertFunc := converter.IndexConvertFuncWithHook(
		convertFunc,
		d.docker2oci,
		d.platformMC,
		convertHooks,
	)

	return indexConvertFunc(ctx, cs, source)
}

func (d *Driver) makeManifestIndex(ctx context.Context, cs content.Store, oci, nydus ocispec.Descriptor) (*ocispec.Descriptor, error) {
	ociDescs, err := utils.GetManifests(ctx, cs, oci, d.platformMC)
	if err != nil {
		return nil, errors.Wrap(err, "get oci image manifest list")
	}
	for idx, desc := range ociDescs {
		// Modify initial OCI image to prevent layer reuse with non-nydus OCI images
		desc, err = PrependEmptyLayer(ctx, cs, desc)
		if err != nil {
			return nil, errors.Wrap(err, "prepend empty layer")
		}
		ociDescs[idx] = desc
	}

	nydusDescs, err := utils.GetManifests(ctx, cs, nydus, d.platformMC)
	if err != nil {
		return nil, errors.Wrap(err, "get nydus image manifest list")
	}
	for idx, desc := range nydusDescs {
		if desc.Platform == nil {
			desc.Platform = &ocispec.Platform{}
		}
		desc.ArtifactType = nydusutils.ArtifactTypeNydusImage
		nydusDescs[idx] = desc
	}

	descs := append(ociDescs, nydusDescs...)

	index := ocispec.Index{
		Versioned: specs.Versioned{
			SchemaVersion: 2,
		},
		Manifests: descs,
	}

	indexDesc, indexBytes, err := nydusutils.MarshalToDesc(index, ocispec.MediaTypeImageIndex)
	if err != nil {
		return nil, errors.Wrap(err, "marshal image manifest index")
	}

	labels := map[string]string{}
	for idx, desc := range descs {
		labels[fmt.Sprintf("containerd.io/gc.ref.content.%d", idx)] = desc.Digest.String()
	}
	if err := content.WriteBlob(
		ctx, cs, indexDesc.Digest.String(), bytes.NewReader(indexBytes), *indexDesc, content.WithLabels(labels),
	); err != nil {
		return nil, errors.Wrap(err, "write image manifest")
	}

	return indexDesc, nil
}

func (d *Driver) getChunkDict(ctx context.Context, provider accelcontent.Provider) (*chunkDictInfo, error) {
	if d.chunkDictRef == "" {
		return nil, nil
	}

	parser, err := parser.New(provider)
	if err != nil {
		return nil, errors.Wrap(err, "create chunk dict parser")
	}

	bootstrapReader, _, err := parser.PullAsChunkDict(ctx, d.chunkDictRef, false)
	if err != nil {
		if errdefs.NeedsRetryWithHTTP(err) {
			logrus.Infof("try to pull chunk dict image with plain HTTP for %s", d.chunkDictRef)
			bootstrapReader, _, err = parser.PullAsChunkDict(ctx, d.chunkDictRef, true)
			if err != nil {
				return nil, errors.Wrapf(err, "try to pull chunk dict image %s", d.chunkDictRef)
			}
		} else {
			return nil, errors.Wrapf(err, "pull chunk dict image %s", d.chunkDictRef)
		}
	}
	defer bootstrapReader.Close()

	bootstrapFile, err := os.CreateTemp(d.workDir, "nydus-chunk-dict-")
	if err != nil {
		return nil, errors.Wrapf(err, "create temp file for chunk dict bootstrap")
	}
	defer bootstrapFile.Close()

	bootstrapPath := bootstrapFile.Name()
	// FIXME: avoid unpacking the bootstrap on every conversion.
	if err := nydusutils.UnpackFile(content.NewReader(bootstrapReader), nydusutils.BootstrapFileNameInLayer, bootstrapFile.Name()); err != nil {
		return nil, errors.Wrap(err, "unpack nydus bootstrap")
	}

	chunkDict := chunkDictInfo{
		BootstrapPath: bootstrapPath,
	}

	return &chunkDict, nil
}

// PrependEmptyLayer modifies the original image manifest and config to prepend an empty layer
// This is done on purpose to force new shas for all the subsequent layers when unpacked by runtimes
// So that no layer reuse can be possible between stock OCI images and nydus-converted OCI images
// It returns the updated manifest descriptor
func PrependEmptyLayer(ctx context.Context, cs content.Store, manifestDesc ocispec.Descriptor) (ocispec.Descriptor, error) {
	// Read existing OCI manifest
	manifestBytes, err := content.ReadBlob(ctx, cs, manifestDesc)
	if err != nil {
		return ocispec.Descriptor{}, errors.Wrap(err, "read manifest")
	}

	var manifest ocispec.Manifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return ocispec.Descriptor{}, errors.Wrap(err, "unmarshal manifest")
	}

	// Read existing OCI config
	configBytes, err := content.ReadBlob(ctx, cs, manifest.Config)
	if err != nil {
		return ocispec.Descriptor{}, errors.Wrap(err, "read config")
	}

	var config ocispec.Image
	if err := json.Unmarshal(configBytes, &config); err != nil {
		return ocispec.Descriptor{}, errors.Wrap(err, "unmarshal config")
	}

	// Rebuild the layer list with an empty layer at the beginning
	// This will force new shas for all the subsequent layers
	var (
		emptyLayerMediaType       string
		configDescriptorMediaType string
	)

	switch manifest.MediaType {
	case ocispec.MediaTypeImageManifest:
		emptyLayerMediaType = ocispec.MediaTypeImageLayerGzip
		configDescriptorMediaType = ocispec.MediaTypeImageConfig
	case images.MediaTypeDockerSchema2Manifest, images.MediaTypeDockerSchema1Manifest:
		emptyLayerMediaType = images.MediaTypeDockerSchema2LayerGzip
		configDescriptorMediaType = images.MediaTypeDockerSchema2Config
	}
	emptyDescriptorBytes := generateDockerEmptyLayer()
	emptyDescriptor := ocispec.Descriptor{
		MediaType: emptyLayerMediaType,
		Digest:    digest.FromBytes(emptyDescriptorBytes),
		Size:      int64(len(emptyDescriptorBytes)),
	}

	manifest.Layers = append([]ocispec.Descriptor{emptyDescriptor}, manifest.Layers...)
	if manifest.Annotations == nil {
		manifest.Annotations = map[string]string{}
	}
	manifest.Annotations[annotationSourceDigest] = manifestDesc.Digest.String()
	// Add an empty diff_id at the beginning of the config
	config.RootFS.DiffIDs = append([]digest.Digest{emptyTarGzipUnpackedDigest}, config.RootFS.DiffIDs...)
	// Rewrite history to add an entry for the empty layer
	createdTime := time.Now()
	emptyLayerHistory := ocispec.History{
		Created:   &createdTime,
		CreatedBy: "Nydus Converter",
		Comment:   "Nydus Empty Layer",
	}
	config.History = append([]ocispec.History{emptyLayerHistory}, config.History...)

	newConfigDesc, newConfigBytes, err := nydusutils.MarshalToDesc(config, configDescriptorMediaType)
	if err != nil {
		return ocispec.Descriptor{}, errors.Wrap(err, "marshal modified config")
	}
	if newConfigDesc.Annotations == nil {
		newConfigDesc.Annotations = map[string]string{}
	}
	newConfigDesc.Annotations[annotationSourceDigest] = manifest.Config.Digest.String()

	manifest.Config = *newConfigDesc
	newManifestDesc, newManifestBytes, err := nydusutils.MarshalToDesc(manifest, manifest.MediaType)
	if err != nil {
		return ocispec.Descriptor{}, errors.Wrap(err, "marshal modified manifest")
	}
	// Add back the original information of the manifest descriptor
	newManifestDesc.Platform = manifestDesc.Platform
	newManifestDesc.URLs = manifestDesc.URLs
	newManifestDesc.ArtifactType = manifestDesc.ArtifactType
	newManifestDesc.Annotations = manifestDesc.Annotations

	if newManifestDesc.Annotations == nil {
		newManifestDesc.Annotations = map[string]string{}
	}
	newManifestDesc.Annotations[annotationSourceDigest] = manifestDesc.Digest.String()

	// Write modified config
	if err := content.WriteBlob(
		ctx, cs, newConfigDesc.Digest.String(), bytes.NewReader(newConfigBytes), *newConfigDesc,
	); err != nil {
		return ocispec.Descriptor{}, errors.Wrap(err, "write modified config")
	}

	// Write empty blob
	if err := content.WriteBlob(
		ctx, cs, emptyDescriptor.Digest.String(), bytes.NewReader(emptyDescriptorBytes), emptyDescriptor,
	); err != nil {
		return ocispec.Descriptor{}, errors.Wrap(err, "write empty json blob")
	}

	// Write modified manifest
	if err := content.WriteBlob(
		ctx, cs, newManifestDesc.Digest.String(), bytes.NewReader(newManifestBytes), *newManifestDesc,
	); err != nil {
		return ocispec.Descriptor{}, errors.Wrap(err, "write modified manifest")
	}

	return *newManifestDesc, nil
}

// Empty gzip-compressed tar file that can be used as an empty layer content
func generateDockerEmptyLayer() []byte {
	var buf bytes.Buffer
	gzw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gzw)
	tw.Close()
	gzw.Close()
	return buf.Bytes()
}
