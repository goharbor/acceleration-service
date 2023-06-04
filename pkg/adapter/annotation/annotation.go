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

package annotation

import (
	"context"
	"fmt"

	"github.com/containerd/containerd/images"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"

	providerContent "github.com/goharbor/acceleration-service/pkg/content"
	nydusutils "github.com/goharbor/acceleration-service/pkg/driver/nydus/utils"
	"github.com/goharbor/acceleration-service/pkg/utils"
)

type Appended struct {
	DriverName       string
	DriverVersion    string
	SourceDigest     string
	ExtraAnnotations map[string]string
}

const (
	// The name annotation is used to identify different accelerated image formats in harbor.
	// Example value: `nydus`, `estargz`.
	AnnotationAccelerationDriverName = "io.goharbor.artifact.v1alpha1.acceleration.driver.name"
	// The version annotation is used to identify different accelerated image format versions
	// with same driver in harbor.
	AnnotationAccelerationDriverVersion = "io.goharbor.artifact.v1alpha1.acceleration.driver.version"
	// The digest annotation is used to reference the source (original) image, which can be
	// used to avoid duplicate conversion or track the relationship between the source image
	// and converted image in harbor.
	// Example value: `sha256:2d64e20e048640ecb619b82a26c168b7649a173d4ad6cf2af3feda9b64fe6fb8`.
	AnnotationAccelerationSourceDigest = "io.goharbor.artifact.v1alpha1.acceleration.source.digest"
)

func annotate(annotations map[string]string, appended Appended) map[string]string {
	if annotations == nil {
		annotations = make(map[string]string)
	}

	annotations[AnnotationAccelerationDriverName] = appended.DriverName
	if appended.DriverVersion != "" {
		annotations[AnnotationAccelerationDriverVersion] = appended.DriverVersion
	}
	if appended.SourceDigest != "" {
		annotations[AnnotationAccelerationSourceDigest] = appended.SourceDigest
	}

	for k, v := range appended.ExtraAnnotations {
		annotations[k] = v
	}

	return annotations
}

func Append(ctx context.Context, provider providerContent.Provider, desc *ocispec.Descriptor, appended Appended) (*ocispec.Descriptor, error) {
	var labels map[string]string
	var err error

	switch desc.MediaType {
	// Refer: https://github.com/goharbor/harbor/blob/main/src/controller/artifact/abstractor.go#L75
	case ocispec.MediaTypeImageManifest, images.MediaTypeDockerSchema2Manifest:
		var manifest ocispec.Manifest
		labels, err = utils.ReadJSON(ctx, provider.ContentStore(), &manifest, *desc)
		if err != nil {
			return nil, errors.Wrap(err, "read manifest")
		}

		manifest.Annotations = annotate(manifest.Annotations, appended)
		desc, err := utils.WriteJSON(ctx, provider.ContentStore(), manifest, *desc, "", labels)
		if err != nil {
			return nil, errors.Wrap(err, "write manifest")
		}

		return desc, nil

	// Refer: https://github.com/goharbor/harbor/blob/main/src/controller/artifact/abstractor.go#L79
	case ocispec.MediaTypeImageIndex, images.MediaTypeDockerSchema2ManifestList:
		var index ocispec.Index
		labels, err = utils.ReadJSON(ctx, provider.ContentStore(), &index, *desc)
		if err != nil {
			return nil, errors.Wrap(err, "read manifest index")
		}

		for idx, maniDesc := range index.Manifests {
			if maniDesc.Platform != nil && maniDesc.Platform.OSFeatures[0] == nydusutils.ManifestOSFeatureNydus {
				var manifest ocispec.Manifest
				labels, err = utils.ReadJSON(ctx, provider.ContentStore(), &manifest, maniDesc)
				if err != nil {
					return nil, errors.Wrap(err, "read manifest")
				}

				manifest.Annotations = annotate(maniDesc.Annotations, appended)
				newManiDesc, err := utils.WriteJSON(ctx, provider.ContentStore(), manifest, maniDesc, "", labels)
				if err != nil {
					return nil, errors.Wrap(err, "write manifest")
				}
				index.Manifests[idx] = *newManiDesc
			}
		}
		desc, err := utils.WriteJSON(ctx, provider.ContentStore(), index, *desc, "", labels)
		if err != nil {
			return nil, errors.Wrap(err, "write manifest index")
		}

		return desc, nil
	}

	return nil, fmt.Errorf("invalid mediatype %s", desc.MediaType)
}
