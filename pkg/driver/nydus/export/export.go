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

package export

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"

	imageContent "github.com/containerd/containerd/content"
	"github.com/goharbor/acceleration-service/pkg/content"
	"github.com/goharbor/acceleration-service/pkg/driver/nydus/packer"
	"github.com/goharbor/acceleration-service/pkg/driver/nydus/utils"
)

// Export exports all Nydus layer descriptors to a image manifest, then writes
// to content store, and returns Nydus image manifest.
func Export(ctx context.Context, content content.Provider, layers []packer.Descriptor, hasBackend bool) (*ocispec.Descriptor, error) {
	if len(layers) <= 0 {
		return nil, fmt.Errorf("can't export image with empty layers")
	}

	// Export image config.
	ociConfigDesc, err := content.Image().Config(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "get source image config description")
	}
	configBytes, err := imageContent.ReadBlob(ctx, content.ContentStore(), ociConfigDesc)
	if err != nil {
		return nil, errors.Wrap(err, "read source image config")
	}
	var nydusConfig ocispec.Image
	if err := json.Unmarshal(configBytes, &nydusConfig); err != nil {
		return nil, errors.Wrap(err, "unmarshal source image config")
	}
	nydusConfig.RootFS.DiffIDs = []digest.Digest{}
	nydusConfig.History = []ocispec.History{}

	descs := []ocispec.Descriptor{}
	finalLayer := layers[len(layers)-1]

	// Append blob layers.
	blobs := []string{}
	for _, blobDesc := range finalLayer.Blobs {
		blobs = append(blobs, blobDesc.Digest.Hex())
		if hasBackend {
			continue
		}
		descs = append(descs, blobDesc)
		nydusConfig.RootFS.DiffIDs = append(nydusConfig.RootFS.DiffIDs, blobDesc.Digest)
		// Remove unnecessary diff id annotation in final manifest.
		delete(blobDesc.Annotations, utils.LayerAnnotationUncompressed)
	}

	// Append bootstrap layer.
	layerDiffID := digest.Digest(finalLayer.Bootstrap.Annotations[utils.LayerAnnotationUncompressed])
	nydusConfig.RootFS.DiffIDs = append(nydusConfig.RootFS.DiffIDs, layerDiffID)
	// Remove unnecessary diff id annotation in final manifest.
	delete(finalLayer.Bootstrap.Annotations, utils.LayerAnnotationUncompressed)

	// Append blobs to the annotation of bootstrap layer.
	blobBytes, err := json.Marshal(blobs)
	if err != nil {
		return nil, errors.Wrap(err, "marshal blob list")
	}
	finalLayer.Bootstrap.Annotations[utils.LayerAnnotationNydusBlobIDs] = string(blobBytes)
	descs = append(descs, finalLayer.Bootstrap)

	nydusConfigDesc, nydusConfigBytes, err := utils.MarshalToDesc(nydusConfig, ocispec.MediaTypeImageConfig)
	if err != nil {
		return nil, errors.Wrap(err, "marshal image config")
	}

	if err := imageContent.WriteBlob(
		ctx, content.ContentStore(), nydusConfigDesc.Digest.String(), bytes.NewReader(nydusConfigBytes), *nydusConfigDesc,
	); err != nil {
		return nil, errors.Wrap(err, "write image config")
	}

	// Export image manifest.
	nydusManifest := struct {
		MediaType string `json:"mediaType,omitempty"`
		ocispec.Manifest
	}{
		MediaType: ocispec.MediaTypeImageManifest,
		Manifest: ocispec.Manifest{
			Versioned: specs.Versioned{
				SchemaVersion: 2,
			},
			Config: *nydusConfigDesc,
			Layers: descs,
		},
	}
	nydusManifestDesc, manifestBytes, err := utils.MarshalToDesc(nydusManifest, ocispec.MediaTypeImageManifest)
	if err != nil {
		return nil, errors.Wrap(err, "marshal image manifest")
	}

	labels := map[string]string{}
	labels["containerd.io/gc.ref.content.0"] = nydusConfigDesc.Digest.String()
	for idx, desc := range descs {
		labels[fmt.Sprintf("containerd.io/gc.ref.content.%d", idx+1)] = desc.Digest.String()
	}

	if err := imageContent.WriteBlob(
		ctx, content.ContentStore(), nydusManifestDesc.Digest.String(), bytes.NewReader(manifestBytes), *nydusManifestDesc, imageContent.WithLabels(labels),
	); err != nil {
		return nil, errors.Wrap(err, "write image manifest")
	}

	return nydusManifestDesc, nil
}
