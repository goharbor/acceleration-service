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

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"

	"github.com/goharbor/acceleration-service/pkg/utils"
)

func annotate(annotations map[string]string, appended map[string]string) map[string]string {
	if annotations == nil {
		annotations = make(map[string]string)
	}

	for k, v := range appended {
		annotations[k] = v
	}

	return annotations
}

func Append(ctx context.Context, cs content.Store, desc *ocispec.Descriptor, appended map[string]string) (*ocispec.Descriptor, error) {
	if appended == nil {
		return desc, nil
	}

	var labels map[string]string
	var err error

	switch desc.MediaType {
	// Refer: https://github.com/goharbor/harbor/blob/main/src/controller/artifact/abstractor.go#L75
	case ocispec.MediaTypeImageManifest, images.MediaTypeDockerSchema2Manifest:
		var manifest ocispec.Manifest
		labels, err = utils.ReadJSON(ctx, cs, &manifest, *desc)
		if err != nil {
			return nil, errors.Wrap(err, "read manifest")
		}

		manifest.Annotations = annotate(manifest.Annotations, appended)
		desc, err := utils.WriteJSON(ctx, cs, manifest, *desc, "", labels)
		if err != nil {
			return nil, errors.Wrap(err, "write manifest")
		}

		return desc, nil

	// Refer: https://github.com/goharbor/harbor/blob/main/src/controller/artifact/abstractor.go#L79
	case ocispec.MediaTypeImageIndex, images.MediaTypeDockerSchema2ManifestList:
		var index ocispec.Index
		labels, err = utils.ReadJSON(ctx, cs, &index, *desc)
		if err != nil {
			return nil, errors.Wrap(err, "read manifest index")
		}

		for idx, maniDesc := range index.Manifests {
			var manifest ocispec.Manifest
			labels, err = utils.ReadJSON(ctx, cs, &manifest, maniDesc)
			if err != nil {
				return nil, errors.Wrap(err, "read manifest")
			}

			manifest.Annotations = annotate(maniDesc.Annotations, appended)
			newManiDesc, err := utils.WriteJSON(ctx, cs, manifest, maniDesc, "", labels)
			if err != nil {
				return nil, errors.Wrap(err, "write manifest")
			}
			index.Manifests[idx] = *newManiDesc
		}
		desc, err := utils.WriteJSON(ctx, cs, index, *desc, "", labels)
		if err != nil {
			return nil, errors.Wrap(err, "write manifest index")
		}

		return desc, nil
	}

	return nil, fmt.Errorf("invalid mediatype %s", desc.MediaType)
}
