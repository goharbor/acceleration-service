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
	"os"
	"strconv"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	imageContent "github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/platforms"
	digest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/identity"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/goharbor/acceleration-service/pkg/content"
	"github.com/goharbor/acceleration-service/pkg/driver/nydus/backend"
	"github.com/goharbor/acceleration-service/pkg/driver/nydus/export"
	"github.com/goharbor/acceleration-service/pkg/driver/nydus/packer"
	"github.com/goharbor/acceleration-service/pkg/driver/nydus/parser"
	"github.com/goharbor/acceleration-service/pkg/driver/nydus/utils"
)

const supportedOS = "linux"

type Driver struct {
	workDir       string
	backend       backend.Backend
	packer        *packer.Packer
	mergeManifest bool
	rafsVersion   string
	flatten       bool
	chunkDictRef  string
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

	var err error

	_mergeManifest := cfg["merge_manifest"]
	mergeManifest := false
	if _mergeManifest != "" {
		mergeManifest, err = strconv.ParseBool(_mergeManifest)
		if err != nil {
			return nil, fmt.Errorf("invalid merge_manifest option")
		}
	}

	chunkDictRef := cfg["chunk_dict_ref"]

	var _backend backend.Backend
	backendType := cfg["backend_type"]
	backendConfig := cfg["backend_config"]
	if backendType != "" && backendConfig != "" {
		_backend, err = backend.NewBackend(backendType, []byte(backendConfig))
		if err != nil {
			return nil, errors.Wrap(err, "create blob backend")
		}
	}

	rafsVersion := cfg["rafs_version"]
	if rafsVersion == "" {
		rafsVersion = "5"
	}

	flatten := false
	if cfg["flatten"] != "" {
		flatten, err = strconv.ParseBool(cfg["flatten"])
		if err != nil {
			return nil, fmt.Errorf("invalid flatten option")
		}
	}

	p, err := packer.New(packer.Option{
		WorkDir:     workDir,
		BuilderPath: builderPath,
		RafsVersion: rafsVersion,
		Flatten:     flatten,
	})
	if err != nil {
		return nil, errors.Wrap(err, "create nydus packer")
	}

	return &Driver{
		workDir:       workDir,
		packer:        p,
		backend:       _backend,
		mergeManifest: mergeManifest,
		rafsVersion:   rafsVersion,
		flatten:       flatten,
		chunkDictRef:  chunkDictRef,
	}, nil
}

func (nydus *Driver) makeManifestIndex(ctx context.Context, content content.Provider, descs []ocispec.Descriptor) (*ocispec.Descriptor, error) {
	index := ocispec.Index{
		Versioned: specs.Versioned{
			SchemaVersion: 2,
		},
		Manifests: descs,
	}

	indexDesc, indexBytes, err := utils.MarshalToDesc(index, ocispec.MediaTypeImageIndex)
	if err != nil {
		return nil, errors.Wrap(err, "marshal image manifest index")
	}

	labels := map[string]string{}
	for idx, desc := range descs {
		labels[fmt.Sprintf("containerd.io/gc.ref.content.%d", idx)] = desc.Digest.String()
	}
	if err := imageContent.WriteBlob(
		ctx, content.ContentStore(), indexDesc.Digest.String(), bytes.NewReader(indexBytes), *indexDesc, imageContent.WithLabels(labels),
	); err != nil {
		return nil, errors.Wrap(err, "write image manifest")
	}

	return indexDesc, nil
}

func (nydus *Driver) getChunkDict(ctx context.Context, content content.Provider) (*packer.ChunkDict, error) {
	if nydus.chunkDictRef == "" {
		return nil, nil
	}

	parser, err := parser.New(content)
	if err != nil {
		return nil, errors.Wrap(err, "create chunk dict parser")
	}
	bootstrapReader, blobs, err := parser.PullAsChunkDict(ctx, nydus.chunkDictRef)
	if err != nil {
		return nil, errors.Wrapf(err, "pull chunk dict image %s", nydus.chunkDictRef)
	}
	defer bootstrapReader.Close()

	bootstrapFile, err := os.CreateTemp(nydus.workDir, "nydus-chunk-dict-")
	if err != nil {
		return nil, errors.Wrapf(err, "create temp file for chunk dict bootstrap")
	}
	defer bootstrapFile.Close()

	bootstrapPath := bootstrapFile.Name()
	// FIXME: avoid unpacking the bootstrap on every conversion.
	if err := utils.UnpackFile(imageContent.NewReader(bootstrapReader), utils.BootstrapFileNameInLayer, bootstrapFile.Name()); err != nil {
		return nil, errors.Wrap(err, "unpack nydus bootstrap")
	}

	chunkDict := packer.ChunkDict{
		BootstrapPath: bootstrapPath,
		Blobs:         blobs,
	}

	return &chunkDict, nil
}

func (nydus *Driver) convert(ctx context.Context, src ocispec.Manifest, content content.Provider) (*ocispec.Descriptor, error) {
	diffIDs, err := content.Image().RootFS(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "get diff ids from containerd")
	}

	layers := []packer.Layer{}

	var chain []digest.Digest
	for idx := range src.Layers {
		chain = append(chain, diffIDs[idx])
		upper := identity.ChainID(chain).String()

		layers = append(layers, &buildLayer{
			chainID: upper,
			sn:      content.Snapshotter(),
			cs:      content.ContentStore(),
			backend: nydus.backend,
		})
	}

	if nydus.flatten && len(src.Layers) > 0 {
		layers = layers[len(layers)-1:]
	}

	chunkDict, err := nydus.getChunkDict(ctx, content)
	if err != nil {
		return nil, errors.Wrap(err, "get chunk dict")
	}
	if chunkDict != nil {
		defer os.Remove(chunkDict.BootstrapPath)
	}

	nydusLayers, err := nydus.packer.Build(ctx, chunkDict, layers)
	if err != nil {
		return nil, errors.Wrap(err, "build nydus image")
	}

	hasBackend := nydus.backend != nil
	desc, err := export.Export(ctx, content, nydusLayers, hasBackend)
	if err != nil {
		return nil, errors.Wrap(err, "export nydus manifest")
	}

	return desc, nil
}

func (nydus *Driver) Convert(ctx context.Context, content content.Provider) (*ocispec.Descriptor, error) {
	provider := content.Image().ContentStore()

	descs, err := utils.GetManifests(ctx, provider, content.Image().Target())
	if err != nil {
		return nil, errors.Wrap(err, "get image manifest list")
	}
	targetDescs := []ocispec.Descriptor{}

	for _, srcDesc := range descs {
		manifest, err := images.Manifest(ctx, provider, srcDesc, platforms.All)
		if err != nil {
			return nil, errors.Wrap(err, "get image manifest")
		}

		configBytes, err := imageContent.ReadBlob(ctx, provider, manifest.Config)
		if err != nil {
			return nil, err
		}

		platform := srcDesc.Platform
		if platform == nil {
			var srcConfig ocispec.Image
			if err := json.Unmarshal(configBytes, &srcConfig); err != nil {
				return nil, err
			}
			_platform := platforms.Normalize(ocispec.Platform{
				Architecture: srcConfig.Architecture,
				OS:           srcConfig.OS,
				OSVersion:    srcConfig.OSVersion,
				OSFeatures:   srcConfig.OSFeatures,
				Variant:      srcConfig.Variant,
			})
			platform = &_platform
		}

		if platform.OS != supportedOS {
			logrus.Warnf("skip unsupported platform: %v", platform)
			continue
		}

		if utils.IsNydusPlatform(platform) || utils.IsNydusManifest(&manifest) {
			// Skip the conversion of existing nydus manifest.
			logrus.Warnf("skip existing nydus manifest: %v", platform)
			continue
		}

		if nydus.mergeManifest {
			// Ensure that both OCIv1 manifest and nydus manifest are present as
			// manifest index in the target image. The nydus manifest is marked
			// in platform field with `"os.features": [ "nydus.remoteimage.v1" ]`.
			// Example: https://github.com/dragonflyoss/image-service/blob/d3e16a4434ec58886531a3348efc1a25dac6ede9/contrib/nydusify/examples/manifest/index.json
			targetDescs = append(targetDescs, srcDesc)
		}

		nydusDesc, err := nydus.convert(ctx, manifest, content)
		if err != nil {
			return nil, errors.Wrapf(err, "convert for platform %v", platform)
		}

		if nydus.mergeManifest {
			if nydusDesc.Platform == nil {
				nydusDesc.Platform = platform
			}
			nydusDesc.Platform.OSFeatures = []string{utils.ManifestOSFeatureNydus}
		}

		targetDescs = append(targetDescs, *nydusDesc)
	}

	if len(targetDescs) == 0 {
		return nil, fmt.Errorf("not found supported platform")
	}

	if len(targetDescs) == 1 {
		return &targetDescs[0], nil
	}

	return nydus.makeManifestIndex(ctx, content, targetDescs)
}

func (nydus *Driver) Name() string {
	return "nydus"
}

func (nydus *Driver) Version() string {
	return nydus.rafsVersion
}
