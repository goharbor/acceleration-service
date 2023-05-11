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

package converter

import (
	"context"
	"fmt"
	"time"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	MediaTypeDockerSchema2Manifest     = "application/vnd.docker.distribution.manifest.v2+json"
	MediaTypeDockerSchema2ManifestList = "application/vnd.docker.distribution.manifest.list.v2+json"
)

// Metric collected the metrics of conversion progress
type Metric struct {
	// Total size of the source image with specified platforms in bytes
	SourceImageSize int64
	// Total size of the target image with specified platforms in bytes
	TargetImageSize int64
	// Elapsed time of pulling source image
	SourcePullElapsed time.Duration
	// Elapsed time of pushing target image
	ConversionElapsed time.Duration
	// Elapsed time of converting source image to target image
	TargetPushElapsed time.Duration
}

func (metric *Metric) SetTargetImageSize(ctx context.Context, cs content.Store, desc *ocispec.Descriptor) error {
	var err error
	metric.TargetImageSize, err = metric.imageSize(ctx, cs, desc)
	return err
}

func (metric *Metric) SetSourceImageSize(ctx context.Context, cvt *Converter, source string) error {
	image, err := cvt.provider.Image(ctx, source)
	if err != nil {
		return err
	}
	metric.SourceImageSize, err = metric.imageSize(ctx, cvt.provider.ContentStore(), image)
	return err
}

func (metric *Metric) imageSize(ctx context.Context, cs content.Store, image *ocispec.Descriptor) (int64, error) {
	var imageSize int64
	switch image.MediaType {
	case ocispec.MediaTypeImageIndex, MediaTypeDockerSchema2ManifestList:
		manifests, err := images.ChildrenHandler(cs)(ctx, *image)
		if err != nil {
			return imageSize, err
		}
		for _, manifest := range manifests {
			children, err := images.ChildrenHandler(cs)(ctx, manifest)
			if err != nil {
				return imageSize, err
			}
			for _, desc := range children {
				imageSize += desc.Size
			}
		}
	case ocispec.MediaTypeImageManifest, MediaTypeDockerSchema2Manifest:
		children, err := images.ChildrenHandler(cs)(ctx, *image)
		if err != nil {
			return imageSize, err
		}
		for _, desc := range children {
			imageSize += desc.Size
		}
	default:
		return imageSize, fmt.Errorf("unknown descriptor type %s", image.MediaType)
	}
	return imageSize, nil
}
