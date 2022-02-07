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
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"

	providerContent "github.com/goharbor/acceleration-service/pkg/content"
)

type Appended struct {
	DriverName    string
	DriverVersion string
	SourceDigest  string
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

// Ported from containerd project, copyright The containerd Authors.
// https://github.com/containerd/containerd/blob/26d356d09de89b609cb75562fd87da6aa3c70740/images/converter/default.go#L385
func readJSON(ctx context.Context, cs content.Store, x interface{}, desc ocispec.Descriptor) (map[string]string, error) {
	info, err := cs.Info(ctx, desc.Digest)
	if err != nil {
		return nil, err
	}

	labels := info.Labels
	b, err := content.ReadBlob(ctx, cs, desc)
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(b, x); err != nil {
		return nil, err
	}

	return labels, nil
}

// Modified from containerd project, copyright The containerd Authors.
// https://github.com/containerd/containerd/blob/26d356d09de89b609cb75562fd87da6aa3c70740/images/converter/default.go#L401
func writeJSON(ctx context.Context, cs content.Store, x interface{}, oldDesc ocispec.Descriptor, labels map[string]string) (*ocispec.Descriptor, error) {
	b, err := json.MarshalIndent(x, "", "  ")
	if err != nil {
		return nil, err
	}
	dgst := digest.SHA256.FromBytes(b)

	newDesc := oldDesc
	newDesc.Size = int64(len(b))
	newDesc.Digest = dgst

	if err := content.WriteBlob(ctx, cs, dgst.String(), bytes.NewReader(b), newDesc, content.WithLabels(labels)); err != nil {
		return nil, err
	}

	return &newDesc, nil
}

func annotate(annotations map[string]string, appended Appended) map[string]string {
	if annotations == nil {
		annotations = make(map[string]string)
	}

	annotations[AnnotationAccelerationDriverName] = appended.DriverName
	if appended.DriverVersion != "" {
		annotations[AnnotationAccelerationDriverVersion] = appended.DriverVersion
	}
	annotations[AnnotationAccelerationSourceDigest] = appended.SourceDigest

	return annotations
}

func Append(ctx context.Context, provider providerContent.Provider, desc *ocispec.Descriptor, appended Appended) (*ocispec.Descriptor, error) {
	var labels map[string]string
	var err error

	switch desc.MediaType {
	case ocispec.MediaTypeImageManifest:
		var manifest ocispec.Manifest
		labels, err = readJSON(ctx, provider.ContentStore(), &manifest, *desc)
		if err != nil {
			return nil, errors.Wrap(err, "read manifest")
		}

		manifest.Annotations = annotate(manifest.Annotations, appended)
		desc, err := writeJSON(ctx, provider.ContentStore(), manifest, *desc, labels)
		if err != nil {
			return nil, errors.Wrap(err, "write manifest")
		}

		return desc, nil

	case ocispec.MediaTypeImageIndex:
		var index ocispec.Index
		labels, err = readJSON(ctx, provider.ContentStore(), &index, *desc)
		if err != nil {
			return nil, errors.Wrap(err, "read manifest index")
		}

		index.Annotations = annotate(index.Annotations, appended)
		desc, err := writeJSON(ctx, provider.ContentStore(), index, *desc, labels)
		if err != nil {
			return nil, errors.Wrap(err, "write manifest index")
		}

		return desc, nil

	case images.MediaTypeDockerSchema2Manifest, images.MediaTypeDockerSchema2ManifestList:
		return nil, fmt.Errorf("docker manifest not support to append annotation")
	}

	return nil, fmt.Errorf("invalid mediatype %s", desc.MediaType)
}
