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

package parser

import (
	"context"

	imageContent "github.com/containerd/containerd/v2/core/content"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"

	"github.com/goharbor/acceleration-service/pkg/content"
	nydusUtils "github.com/goharbor/acceleration-service/pkg/driver/nydus/utils"
	"github.com/goharbor/acceleration-service/pkg/utils"
)

type Parser struct {
	content content.Provider
}

func New(content content.Provider) (*Parser, error) {
	return &Parser{
		content: content,
	}, nil
}

func (parser *Parser) PullAsChunkDict(ctx context.Context, ref string, usePlainHTTP bool) (imageContent.ReaderAt, map[string]ocispec.Descriptor, error) {
	cs := parser.content.ContentStore()

	if usePlainHTTP {
		parser.content.UsePlainHTTP()
	}

	if err := parser.content.Pull(ctx, ref); err != nil {
		return nil, nil, errors.Wrap(err, "pull chunk dict image")
	}

	image, err := parser.content.Image(ctx, ref)
	if err != nil {
		return nil, nil, errors.Wrap(err, "get image from content store")
	}

	manifest := ocispec.Manifest{}
	_, err = utils.ReadJSON(ctx, cs, &manifest, *image)
	if err != nil {
		return nil, nil, errors.Wrap(err, "read manifest json")
	}

	var bootstrapDesc *ocispec.Descriptor
	blobs := make(map[string]ocispec.Descriptor)
	for _, desc := range manifest.Layers {
		if desc.Annotations == nil {
			continue
		}
		if desc.Annotations[nydusUtils.LayerAnnotationNydusBootstrap] != "" {
			bootstrapDesc = &desc
		} else if desc.Annotations[nydusUtils.LayerAnnotationNydusBlob] != "" {
			blobs[desc.Digest.Hex()] = desc
		}
	}

	ra, err := cs.ReaderAt(ctx, *bootstrapDesc)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "fetch bootstrap %s", bootstrapDesc.Digest)
	}

	return ra, blobs, nil
}
