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

	"github.com/containerd/containerd"
	imageContent "github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/goharbor/acceleration-service/pkg/content"
	nydusUtils "github.com/goharbor/acceleration-service/pkg/driver/nydus/utils"
	"github.com/goharbor/acceleration-service/pkg/utils"
)

var logger = logrus.WithField("module", "nydus-driver")

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
	resolver, err := parser.content.Resolver(ctx, ref)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "get resolver for %s", ref)
	}
	opts := []containerd.RemoteOpt{
		containerd.WithPlatformMatcher(nydusUtils.NydusPlatformComparer{}),
		containerd.WithImageHandler(images.HandlerFunc(
			func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
				if images.IsLayerType(desc.MediaType) {
					logger.Debugf("pulling chunk dict image layer %s", desc.Digest)
				}
				return nil, nil
			},
		)),
		containerd.WithResolver(resolver),
	}
	image, err := parser.content.Client().Fetch(ctx, ref, opts...)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "pull chunk dict image %s", ref)
	}

	manifest := ocispec.Manifest{}
	_, err = utils.ReadJSON(ctx, cs, &manifest, image.Target)
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
