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
	"os"
	"path"

	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"

	imageContent "github.com/containerd/containerd/content"
	"github.com/goharbor/acceleration-service/pkg/content"
	"github.com/goharbor/acceleration-service/pkg/driver/nydus/utils"
)

// WriteBlob writes Nydus blob data to content store, and returns
// blob layer descriptor.
func WriteBlob(
	ctx context.Context, content content.Provider, blobPath string,
) (*ocispec.Descriptor, error) {
	blobFile, err := os.Open(blobPath)
	if err != nil {
		return nil, errors.Wrapf(err, "open blob %s", blobPath)
	}
	defer blobFile.Close()

	blobStat, err := blobFile.Stat()
	if err != nil {
		return nil, errors.Wrapf(err, "stat blob %s", blobPath)
	}

	blobID := path.Base(blobPath)
	blobDigest := digest.NewDigestFromEncoded(digest.SHA256, blobID)
	desc := ocispec.Descriptor{
		Digest:    blobDigest,
		Size:      blobStat.Size(),
		MediaType: MediaTypeNydusBlob,
		Annotations: map[string]string{
			// Use `LayerAnnotationUncompressed` to generate
			// DiffID of layer defined in OCI spec.
			LayerAnnotationUncompressed: blobDigest.String(),
			LayerAnnotationNydusBlob:    "true",
		},
	}

	if err := imageContent.WriteBlob(
		ctx, content.ContentStore(), desc.Digest.String(), blobFile, desc,
	); err != nil {
		return nil, errors.Wrapf(err, "write blob %s to content store", blobPath)
	}

	return &desc, nil
}

// WriteBootstrap writes Nydus bootstrap data to content store, and
// returns bootstrap layer descriptor.
func WriteBootstrap(
	ctx context.Context, content content.Provider, bootstrapPath string, blobs []string,
) (*ocispec.Descriptor, error) {
	bootstrapFile, err := os.Open(bootstrapPath)
	if err != nil {
		return nil, errors.Wrapf(err, "open bootstrap %s", bootstrapPath)
	}
	defer bootstrapFile.Close()

	compressedDigest, compressedSize, err := utils.PackTargzInfo(
		bootstrapPath, BootstrapFileNameInLayer, true,
	)
	if err != nil {
		return nil, errors.Wrap(err, "calculate compressed boostrap digest")
	}

	uncompressedDigest, _, err := utils.PackTargzInfo(
		bootstrapPath, BootstrapFileNameInLayer, false,
	)
	if err != nil {
		return nil, errors.Wrap(err, "calculate uncompressed boostrap digest")
	}

	blobListBytes, err := json.Marshal(blobs)
	if err != nil {
		return nil, errors.Wrap(err, "marshal blob list")
	}
	bootstrapMediaType := ocispec.MediaTypeImageLayerGzip
	desc := ocispec.Descriptor{
		Digest:    compressedDigest,
		Size:      compressedSize,
		MediaType: bootstrapMediaType,
		Annotations: map[string]string{
			// Use `utils.LayerAnnotationUncompressed` to generate
			// DiffID of layer defined in OCI spec.
			LayerAnnotationUncompressed:   uncompressedDigest.String(),
			LayerAnnotationNydusBootstrap: "true",
			LayerAnnotationNydusBlobIDs:   string(blobListBytes),
		},
	}

	reader, err := utils.PackTargz(bootstrapPath, BootstrapFileNameInLayer, true)
	if err != nil {
		return nil, errors.Wrap(err, "pack bootstrap to tar.gz")
	}
	defer reader.Close()

	if err := imageContent.WriteBlob(
		ctx, content.ContentStore(), desc.Digest.String(), reader, desc,
	); err != nil {
		return nil, errors.Wrapf(err, "write bootstrap %s to content store", bootstrapPath)
	}

	return &desc, nil
}

// Export exports all Nydus layer descriptors to a image manifest, then writes
// to content store, and returns Nydus image manifest.
func Export(ctx context.Context, content content.Provider, layers []ocispec.Descriptor) (*ocispec.Descriptor, error) {
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

	validAnnotationKeys := map[string]bool{
		LayerAnnotationNydusBlob:      true,
		LayerAnnotationNydusBlobIDs:   true,
		LayerAnnotationNydusBootstrap: true,
	}
	for idx, desc := range layers {
		layerDiffID := digest.Digest(desc.Annotations[LayerAnnotationUncompressed])
		if layerDiffID == "" {
			layerDiffID = desc.Digest
		}
		nydusConfig.RootFS.DiffIDs = append(nydusConfig.RootFS.DiffIDs, layerDiffID)
		if desc.Annotations != nil {
			newAnnotations := make(map[string]string)
			for key, value := range desc.Annotations {
				if validAnnotationKeys[key] {
					newAnnotations[key] = value
				}
			}
			layers[idx].Annotations = newAnnotations
		}
	}
	nydusConfigDesc, nydusConfigBytes, err := utils.MarshalToDesc(nydusConfig, ocispec.MediaTypeImageConfig)
	if err != nil {
		return nil, errors.Wrap(err, "marshal image config")
	}

	if err := imageContent.WriteBlob(
		ctx, content.ContentStore(), nydusConfigDesc.Digest.String(), bytes.NewReader(nydusConfigBytes), *nydusConfigDesc,
	); err != nil {
		return nil, errors.Wrap(err, "write image config")
	}

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
			Layers: layers,
		},
	}
	nydusManifestDesc, manifestBytes, err := utils.MarshalToDesc(nydusManifest, ocispec.MediaTypeImageManifest)
	if err != nil {
		return nil, errors.Wrap(err, "marshal image manifest")
	}

	labels := map[string]string{}
	labels["containerd.io/gc.ref.content.0"] = nydusConfigDesc.Digest.String()
	for i, desc := range layers {
		labels[fmt.Sprintf("containerd.io/gc.ref.content.%d", i+1)] = desc.Digest.String()
	}

	if err := imageContent.WriteBlob(
		ctx, content.ContentStore(), nydusManifestDesc.Digest.String(), bytes.NewReader(manifestBytes), *nydusManifestDesc, imageContent.WithLabels(labels),
	); err != nil {
		return nil, errors.Wrap(err, "write image manifest")
	}

	return nydusManifestDesc, nil
}
