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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/content"
	containerdErrDefs "github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/images/converter"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/nydus-snapshotter/pkg/backend"
	nydusify "github.com/containerd/nydus-snapshotter/pkg/converter"
	"github.com/goharbor/acceleration-service/pkg/adapter/annotation"
	accelcontent "github.com/goharbor/acceleration-service/pkg/content"
	"github.com/goharbor/acceleration-service/pkg/driver/nydus/parser"
	nydusutils "github.com/goharbor/acceleration-service/pkg/driver/nydus/utils"
	"github.com/goharbor/acceleration-service/pkg/errdefs"
	"github.com/goharbor/acceleration-service/pkg/utils"
	"github.com/opencontainers/image-spec/specs-go"
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

	if ociRef && fsVersion != "6" {
		logrus.Warn("forcibly using fs version 6 when oci_ref option enabled")
		fsVersion = "6"
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

	cacheRef := provider.GetCacheRef()
	useRemoteCache := cacheRef != ""
	if useRemoteCache {
		logrus.Infof("remote cache image reference: %s", cacheRef)
		if err := d.FetchRemoteCache(ctx, provider, cacheRef); err != nil {
			if errors.Is(err, errdefs.ErrNotSupport) {
				logrus.Warn("Content store does not support remote cache")
			} else {
				return nil, errors.Wrap(err, "fetch remote cache")
			}
		}
	}

	desc, err := d.convert(ctx, provider, *image, sourceRef)
	if err != nil {
		return nil, err
	}
	if d.mergeManifest {
		return d.makeManifestIndex(ctx, provider.ContentStore(), *image, *desc)
	}

	if useRemoteCache {
		// Fetch the old remote cache before updating and pushing the new one to avoid conflict.
		if err := d.FetchRemoteCache(ctx, provider, cacheRef); err != nil {
			return nil, errors.Wrap(err, "fetch remote cache")
		}
		if err := d.UpdateRemoteCache(ctx, provider, *image, *desc); err != nil {
			return nil, errors.Wrap(err, "update remote cache")
		}
		if err := d.PushRemoteCache(ctx, provider, cacheRef); err != nil {
			return nil, errors.Wrap(err, "push remote cache")
		}
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
	mergeOpt := nydusify.MergeOption{
		WorkDir:           packOpt.WorkDir,
		BuilderPath:       packOpt.BuilderPath,
		FsVersion:         packOpt.FsVersion,
		ChunkDictPath:     packOpt.ChunkDictPath,
		PrefetchPatterns:  packOpt.PrefetchPatterns,
		Backend:           packOpt.Backend,
		OCI:               d.docker2oci,
		OCIRef:            packOpt.OCIRef,
		EncryptRecipients: d.encryptRecipients,
		WithReferrer:      d.withReferrer,
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
	indexConvertFunc := converter.IndexConvertFuncWithHook(
		nydusify.LayerConvertFunc(packOpt),
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

	nydusDescs, err := utils.GetManifests(ctx, cs, nydus, d.platformMC)
	if err != nil {
		return nil, errors.Wrap(err, "get nydus image manifest list")
	}
	for idx, desc := range nydusDescs {
		if desc.Platform == nil {
			desc.Platform = &ocispec.Platform{}
		}
		desc.Platform.OSFeatures = []string{nydusutils.ManifestOSFeatureNydus}
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

// FetchRemoteCache fetch cache manifest from remote
func (d *Driver) FetchRemoteCache(ctx context.Context, pvd accelcontent.Provider, ref string) error {
	resolver, err := pvd.Resolver(ref)
	if err != nil {
		return err
	}

	rc := &containerd.RemoteContext{
		Resolver: resolver,
	}
	name, desc, err := rc.Resolver.Resolve(ctx, ref)
	if err != nil {
		if errors.Is(err, containerdErrDefs.ErrNotFound) {
			// Remote cache may do not exist, just return nil
			return nil
		}
		return err
	}
	fetcher, err := rc.Resolver.Fetcher(ctx, name)
	if err != nil {
		return err
	}
	ir, err := fetcher.Fetch(ctx, desc)
	if err != nil {
		if errdefs.NeedsRetryWithHTTP(err) {
			pvd.UsePlainHTTP()
			ir, err = fetcher.Fetch(ctx, desc)
			if err != nil {
				return errors.Wrap(err, "try to pull remote cache")
			}
		} else {
			return errors.Wrap(err, "pull remote cache")
		}
	}

	bytes, err := io.ReadAll(ir)
	if err != nil {
		return errors.Wrap(err, "read remote cache to bytes")
	}

	// TODO: handle manifest list for multiple platform.
	manifest := ocispec.Manifest{}
	if err = json.Unmarshal(bytes, &manifest); err != nil {
		return err
	}

	cs := pvd.ContentStore()
	for _, layer := range manifest.Layers {
		if _, err := cs.Update(ctx, content.Info{
			Digest: layer.Digest,
			Size:   layer.Size,
			Labels: layer.Annotations,
		}); err != nil {
			if errors.Is(err, containerdErrDefs.ErrNotFound) {
				return errdefs.ErrNotSupport
			}
			return errors.Wrap(err, "update cache layer")
		}
	}
	return nil
}

// PushRemoteCache update cache manifest and push to remote
func (d *Driver) PushRemoteCache(ctx context.Context, pvd accelcontent.Provider, ref string) error {
	imageConfig := ocispec.ImageConfig{}
	imageConfigDesc, imageConfigBytes, err := nydusutils.MarshalToDesc(imageConfig, ocispec.MediaTypeImageConfig)
	if err != nil {
		return errors.Wrap(err, "remote cache image config marshal failed")
	}
	configReader := bytes.NewReader(imageConfigBytes)
	if err = content.WriteBlob(ctx, pvd.ContentStore(), ref, configReader, *imageConfigDesc); err != nil {
		return errors.Wrap(err, "remote cache image config write blob failed")
	}

	cs := pvd.ContentStore()
	layers := []ocispec.Descriptor{}
	if err = cs.Walk(ctx, func(info content.Info) error {
		if _, ok := info.Labels[nydusutils.LayerAnnotationNydusSourceDigest]; ok {
			layers = append(layers, ocispec.Descriptor{
				MediaType:   nydusutils.MediaTypeNydusBlob,
				Digest:      info.Digest,
				Size:        info.Size,
				Annotations: info.Labels,
			})
		}
		return nil
	}); err != nil {
		return errors.Wrap(err, "get remote cache layers failed")
	}

	manifest := ocispec.Manifest{
		Versioned: specs.Versioned{
			SchemaVersion: 2,
		},
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    *imageConfigDesc,
		Layers:    layers,
	}
	manifestDesc, manifestBytes, err := nydusutils.MarshalToDesc(manifest, ocispec.MediaTypeImageManifest)
	if err != nil {
		return errors.Wrap(err, "remote cache manifest marshal failed")
	}
	manifestReader := bytes.NewReader(manifestBytes)
	if err = content.WriteBlob(ctx, pvd.ContentStore(), ref, manifestReader, *manifestDesc); err != nil {
		return errors.Wrap(err, "remote cache write blob failed")
	}

	if err = pvd.Push(ctx, *manifestDesc, ref); err != nil {
		return err
	}
	return nil
}

// UpdateRemoteCache update cache layer from upper to lower
func (d *Driver) UpdateRemoteCache(ctx context.Context, provider accelcontent.Provider, orgDesc ocispec.Descriptor, newDesc ocispec.Descriptor) error {
	cs := provider.ContentStore()

	orgManifest := ocispec.Manifest{}
	_, err := utils.ReadJSON(ctx, cs, &orgManifest, orgDesc)
	if err != nil {
		return errors.Wrap(err, "read original manifest json")
	}

	newManifest := ocispec.Manifest{}
	_, err = utils.ReadJSON(ctx, cs, &newManifest, newDesc)
	if err != nil {
		return errors.Wrap(err, "read new manifest json")
	}
	newLayers := newManifest.Layers[:len(newManifest.Layers)-1]

	// Update <LayerAnnotationNydusSourceDigest> label for each layer
	for i, layer := range newLayers {
		layer.Annotations[nydusutils.LayerAnnotationNydusSourceDigest] = orgManifest.Layers[i].Digest.String()
	}

	// Update cache to lru from upper to lower
	for i := len(newLayers) - 1; i >= 0; i-- {
		layer := newLayers[i]
		if _, err := cs.Update(ctx, content.Info{
			Digest: layer.Digest,
			Size:   layer.Size,
			Labels: layer.Annotations,
		}); err != nil {
			return errors.Wrap(err, "update cache layer")
		}
	}
	return nil
}
