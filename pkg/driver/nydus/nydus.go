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
	"fmt"
	"os"
	"strconv"

	"github.com/goharbor/acceleration-service/pkg/adapter/annotation"
	accelcontent "github.com/goharbor/acceleration-service/pkg/content"
	"github.com/goharbor/acceleration-service/pkg/driver/nydus/parser"
	nydusutils "github.com/goharbor/acceleration-service/pkg/driver/nydus/utils"
	"github.com/goharbor/acceleration-service/pkg/errdefs"
	"github.com/goharbor/acceleration-service/pkg/utils"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images/converter"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/nydus-snapshotter/pkg/backend"
	nydusify "github.com/containerd/nydus-snapshotter/pkg/converter"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

const (
	// annotationNydusFlag is used to indicate a image which is nydus format.
	annotationNydusFlag = "containerd.io/snapshot/nydus"
)

type chunkDictInfo struct {
	BootstrapPath string
}

type Driver struct {
	workDir          string
	builderPath      string
	fsVersion        string
	compressor       string
	chunkDictRef     string
	mergeManifest    bool
	ociRef           bool
	docker2oci       bool
	alignedChunk     bool
	chunkSize        string
	batchSize        string
	prefetchPatterns string
	backend          backend.Backend
	platformMC       platforms.MatchComparer
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

	if ociRef && fsVersion != "6" {
		logrus.Warn("forcibly using fs version 6 when oci_ref option enabled")
		fsVersion = "6"
	}

	return &Driver{
		workDir:          workDir,
		builderPath:      builderPath,
		fsVersion:        fsVersion,
		compressor:       compressor,
		chunkDictRef:     chunkDictRef,
		mergeManifest:    mergeManifest,
		ociRef:           ociRef,
		docker2oci:       docker2oci,
		alignedChunk:     fsAlignChunk,
		chunkSize:        fsChunkSize,
		batchSize:        BatchSize,
		prefetchPatterns: prefetchPatterns,
		backend:          _backend,
		platformMC:       platformMC,
	}, nil
}

func (d *Driver) Name() string {
	return "nydus"
}

func (d *Driver) Version() string {
	return ""
}

func (d *Driver) Convert(ctx context.Context, provider accelcontent.Provider, source string) (*ocispec.Descriptor, error) {
	image, err := provider.Image(ctx, source)
	if err != nil {
		return nil, errors.Wrap(err, "get source image")
	}
	desc, err := d.convert(ctx, provider, *image)
	if err != nil {
		return nil, err
	}
	desc.Annotations = map[string]string{
		annotationNydusFlag:                           "true",
		annotation.AnnotationAccelerationSourceDigest: image.Digest.String(),
	}
	if d.mergeManifest {
		return d.makeManifestIndex(ctx, provider.ContentStore(), *image, *desc)
	}
	return desc, err
}

func (d *Driver) convert(ctx context.Context, provider accelcontent.Provider, source ocispec.Descriptor) (*ocispec.Descriptor, error) {
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
	}
	convertHooks := converter.ConvertHooks{
		PostConvertHook: nydusify.ConvertHookFunc(mergeOpt),
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
