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
	"encoding/json"
	"fmt"
	"io/ioutil"

	imageContent "github.com/containerd/containerd/content"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/remotes"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"

	"github.com/goharbor/acceleration-service/pkg/content"
	"github.com/goharbor/acceleration-service/pkg/driver/nydus/utils"
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

func fetch(ctx context.Context, fetcher remotes.Fetcher, desc ocispec.Descriptor, target interface{}) error {
	rc, err := fetcher.Fetch(ctx, desc)
	if err != nil {
		return errors.Wrapf(err, "fetch %s", desc.Digest)
	}
	defer rc.Close()

	bytes, err := ioutil.ReadAll(rc)
	if err != nil {
		return errors.Wrapf(err, "read manifest")
	}

	if err := json.Unmarshal(bytes, target); err != nil {
		return err
	}

	return nil
}

func (parser *Parser) pull(ctx context.Context, fetcher remotes.Fetcher, desc ocispec.Descriptor) error {
	rc, err := fetcher.Fetch(ctx, desc)
	if err != nil {
		return errors.Wrapf(err, "fetch blob %s", desc.Digest)
	}
	defer rc.Close()

	cw, err := imageContent.OpenWriter(
		ctx, parser.content.ContentStore(),
		imageContent.WithRef(remotes.MakeRefKey(ctx, desc)),
		imageContent.WithDescriptor(desc),
	)
	if err != nil {
		if errdefs.IsAlreadyExists(err) {
			return nil
		}
		return errors.Wrapf(err, "open writer")
	}
	defer cw.Close()

	if err := imageContent.Copy(ctx, cw, rc, desc.Size, desc.Digest); err != nil {
		return errors.Wrapf(err, "pull blob to content store")
	}

	return nil
}

func (parser *Parser) findNydusManifest(ctx context.Context, fetcher remotes.Fetcher, desc ocispec.Descriptor) (*ocispec.Manifest, error) {
	switch desc.MediaType {
	case images.MediaTypeDockerSchema2Manifest, ocispec.MediaTypeImageManifest:
		var manifest ocispec.Manifest
		if err := fetch(ctx, fetcher, desc, &manifest); err != nil {
			return nil, errors.Wrap(err, "fetch manifest")
		}

		if utils.IsNydusManifest(&manifest) {
			return &manifest, nil
		}
	case images.MediaTypeDockerSchema2ManifestList, ocispec.MediaTypeImageIndex:
		var index ocispec.Index
		if err := fetch(ctx, fetcher, desc, &index); err != nil {
			return nil, errors.Wrap(err, "fetch manifest index")
		}

		for _, desc := range index.Manifests {
			if utils.IsNydusPlatform(desc.Platform) {
				return parser.findNydusManifest(ctx, fetcher, desc)
			}
		}
	}

	return nil, fmt.Errorf("not found nydus manifest")
}

func (parser *Parser) PullAsChunkDict(ctx context.Context, ref string) (imageContent.ReaderAt, map[string]ocispec.Descriptor, error) {
	resolver, err := parser.content.Resolver(ctx, ref)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "get resolver for %s", ref)
	}

	_, desc, err := resolver.Resolve(ctx, ref)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "resolve reference %s", ref)
	}

	fetcher, err := resolver.Fetcher(ctx, ref)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "get fetcher for %s", ref)
	}

	manifest, err := parser.findNydusManifest(ctx, fetcher, desc)
	if err != nil {
		return nil, nil, err
	}

	logger.Infof("pulling chunk dict image %s", ref)
	var bootstrapDesc *ocispec.Descriptor
	blobs := make(map[string]ocispec.Descriptor)
	eg := errgroup.Group{}
	for idx := range manifest.Layers {
		layer := manifest.Layers[idx]
		if layer.Annotations == nil {
			continue
		}
		eg.Go(func(desc ocispec.Descriptor) func() error {
			return func() error {
				return errors.Wrapf(parser.pull(ctx, fetcher, desc), "pull chunk dict image layer %s", desc.Digest)
			}
		}(layer))
		if layer.Annotations[utils.LayerAnnotationNydusBootstrap] != "" {
			bootstrapDesc = &layer
		} else if layer.Annotations[utils.LayerAnnotationNydusBlob] != "" {
			blobs[layer.Digest.Hex()] = layer
		}
	}
	if err := eg.Wait(); err != nil {
		return nil, nil, err
	}
	logger.Infof("pulled chunk dict image %s", ref)

	if bootstrapDesc == nil {
		return nil, nil, errors.Errorf("invalid nydus manifest")
	}

	ra, err := parser.content.ContentStore().ReaderAt(ctx, *bootstrapDesc)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "fetch bootstrap %s", bootstrapDesc.Digest)
	}

	return ra, blobs, nil
}
