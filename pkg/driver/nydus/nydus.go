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
	"context"
	"fmt"
	"os"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/images/converter"
	"github.com/containerd/containerd/platforms"
	nydusify "github.com/containerd/nydus-snapshotter/pkg/converter"
	"github.com/goharbor/acceleration-service/pkg/driver/nydus/backend"
	"github.com/goharbor/acceleration-service/pkg/driver/nydus/parser"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"

	accelcontent "github.com/goharbor/acceleration-service/pkg/content"
	nydusutils "github.com/goharbor/acceleration-service/pkg/driver/nydus/utils"
	"github.com/goharbor/acceleration-service/pkg/utils"
)

type chunkDictInfo struct {
	BootstrapPath string
}

type Driver struct {
	workDir      string
	builderPath  string
	fsVersion    string
	compressor   string
	chunkDictRef string
	backend      backend.Backend
}

func New(cfg map[string]string) (*Driver, error) {
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
	if backendType != "" && backendConfig != "" {
		_backend, err = backend.NewBackend(backendType, []byte(backendConfig))
		if err != nil {
			return nil, errors.Wrap(err, "create blob backend")
		}
	}

	fsVersion := cfg["rafs_version"]
	if fsVersion == "" {
		fsVersion = "5"
	}
	compressor := cfg["rafs_compressor"]

	return &Driver{
		workDir:      workDir,
		builderPath:  builderPath,
		fsVersion:    fsVersion,
		compressor:   compressor,
		chunkDictRef: chunkDictRef,
		backend:      _backend,
	}, nil
}

func (d *Driver) Name() string {
	return "nydus"
}

func (d *Driver) Version() string {
	return ""
}

func (d *Driver) Convert(ctx context.Context, provider accelcontent.Provider) (*ocispec.Descriptor, error) {
	cs := provider.ContentStore()

	bootstrapPath := ""
	if d.chunkDictRef != "" {
		chunkDictInfo, err := d.getChunkDict(ctx, provider)
		if err != nil {
			return nil, errors.Wrap(err, "get chunk dict info")
		}
		bootstrapPath = chunkDictInfo.BootstrapPath
	}

	desc, err := converter.DefaultIndexConvertFunc(convertToNydusLayer(nydusify.PackOption{
		FsVersion:     d.fsVersion,
		Compressor:    d.compressor,
		BuilderPath:   d.builderPath,
		WorkDir:       d.workDir,
		ChunkDictPath: bootstrapPath,
	}, d.backend), true, platforms.All)(
		ctx, cs, provider.Image().Target(),
	)
	if err != nil {
		return nil, errors.Wrap(err, "convert to nydus image")
	}

	var labels map[string]string

	convert := func(manifestDesc ocispec.Descriptor) (*ocispec.Descriptor, error) {
		var manifest ocispec.Manifest
		labels, err = utils.ReadJSON(ctx, cs, &manifest, manifestDesc)
		if err != nil {
			return nil, errors.Wrap(err, "read manifest json")
		}

		// Append bootstrap layer to manifest.
		bootstrapDesc, err := mergeNydusLayers(ctx, cs, manifest.Layers, nydusify.MergeOption{
			BuilderPath:   d.builderPath,
			WorkDir:       d.workDir,
			ChunkDictPath: bootstrapPath,
		}, d.fsVersion)
		if err != nil {
			return nil, errors.Wrap(err, "merge nydus layers")
		}
		bootstrapDiffID := digest.Digest(bootstrapDesc.Annotations[nydusutils.LayerAnnotationUncompressed])

		if d.backend != nil {
			// Only append nydus bootstrap layer into manifest, and do not put nydus
			// blob layer into manifest if blob storage backend is specified.
			manifest.Layers = []ocispec.Descriptor{*bootstrapDesc}
		} else {
			manifest.Layers = append(manifest.Layers, *bootstrapDesc)
		}

		// Remove useless annotation.
		for _, layer := range manifest.Layers {
			delete(layer.Annotations, nydusutils.LayerAnnotationUncompressed)
		}

		// Update diff ids in image config.
		var config ocispec.Image
		labels, err = utils.ReadJSON(ctx, cs, &config, manifest.Config)
		if err != nil {
			return nil, errors.Wrap(err, "read image config")
		}
		if d.backend != nil {
			config.RootFS.DiffIDs = []digest.Digest{bootstrapDiffID}
		} else {
			config.RootFS.DiffIDs = append(config.RootFS.DiffIDs, bootstrapDiffID)
		}

		// Update image config in content store.
		newConfigDesc, err := utils.WriteJSON(ctx, cs, config, manifest.Config, "", labels)
		if err != nil {
			return nil, errors.Wrap(err, "write image config")
		}
		manifest.Config = *newConfigDesc

		// Update image manifest in content store.
		newManifestDesc, err := utils.WriteJSON(ctx, cs, manifest, manifestDesc, "", labels)
		if err != nil {
			return nil, errors.Wrap(err, "write manifest")
		}

		return newManifestDesc, nil
	}

	switch desc.MediaType {
	case ocispec.MediaTypeImageManifest:
		newManifestDesc, err := convert(*desc)
		if err != nil {
			return nil, errors.Wrapf(err, "convert manifest %s", desc.Digest)
		}

		return newManifestDesc, nil

	case ocispec.MediaTypeImageIndex:
		var index ocispec.Index
		labels, err = utils.ReadJSON(ctx, cs, &index, *desc)
		if err != nil {
			return nil, errors.Wrap(err, "read manifest index")
		}

		for idx, manifestDesc := range index.Manifests {
			newManifestDesc, err := convert(manifestDesc)
			if err != nil {
				return nil, errors.Wrapf(err, "convert manifest %s", manifestDesc.Digest)
			}
			index.Manifests[idx] = *newManifestDesc
		}

		newIndexDesc, err := utils.WriteJSON(ctx, cs, index, *desc, "", labels)
		if err != nil {
			return nil, errors.Wrap(err, "write manifest index")
		}

		return newIndexDesc, nil

	case images.MediaTypeDockerSchema2Manifest, images.MediaTypeDockerSchema2ManifestList:
		return nil, fmt.Errorf("not support docker manifest")
	}

	return nil, fmt.Errorf("invalid media type %s", desc.MediaType)
}

func (d *Driver) getChunkDict(ctx context.Context, provider accelcontent.Provider) (*chunkDictInfo, error) {
	if d.chunkDictRef == "" {
		return nil, nil
	}

	parser, err := parser.New(provider)
	if err != nil {
		return nil, errors.Wrap(err, "create chunk dict parser")
	}
	bootstrapReader, _, err := parser.PullAsChunkDict(ctx, d.chunkDictRef)
	if err != nil {
		return nil, errors.Wrapf(err, "pull chunk dict image %s", d.chunkDictRef)
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
