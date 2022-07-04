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

	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/images/converter"
	"github.com/containerd/containerd/platforms"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"

	accelcontent "github.com/goharbor/acceleration-service/pkg/content"
	nydusutils "github.com/goharbor/acceleration-service/pkg/driver/nydus/utils"
	"github.com/goharbor/acceleration-service/pkg/utils"
)

type Driver struct {
	cfg map[string]string
}

func New(cfg map[string]string) (*Driver, error) {
	return &Driver{cfg}, nil
}

func (d *Driver) Name() string {
	return "nydus"
}

func (d *Driver) Version() string {
	return ""
}

func (d *Driver) Convert(ctx context.Context, provider accelcontent.Provider) (*ocispec.Descriptor, error) {
	cs := provider.ContentStore()

	desc, err := converter.DefaultIndexConvertFunc(ConvertLayer(), true, platforms.All)(
		ctx, cs, provider.Image().Target(),
	)
	if err != nil {
		return nil, err
	}

	var labels map[string]string

	appendBootstrapLayer := func(manifestDesc ocispec.Descriptor) (*ocispec.Descriptor, error) {
		var manifest ocispec.Manifest
		labels, err = utils.ReadJSON(ctx, cs, &manifest, manifestDesc)

		// Append bootstrap layer to manifest.
		bootstrapDesc, err := MergeNydus(ctx, cs, manifest.Layers)
		if err != nil {
			return nil, err
		}
		bootstrapDiffID := digest.Digest(bootstrapDesc.Annotations[nydusutils.LayerAnnotationUncompressed])
		manifest.Layers = append(manifest.Layers, *bootstrapDesc)

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
		config.RootFS.DiffIDs = append(config.RootFS.DiffIDs, bootstrapDiffID)

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
		newManifestDesc, err := appendBootstrapLayer(*desc)
		if err != nil {
			return nil, err
		}

		return newManifestDesc, nil

	case ocispec.MediaTypeImageIndex:
		var index ocispec.Index
		labels, err = utils.ReadJSON(ctx, cs, &index, *desc)
		if err != nil {
			return nil, errors.Wrap(err, "read manifest index")
		}

		for idx, manifestDesc := range index.Manifests {
			newManifestDesc, err := appendBootstrapLayer(manifestDesc)
			if err != nil {
				return nil, err
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
