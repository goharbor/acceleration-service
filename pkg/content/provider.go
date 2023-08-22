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

package content

import (
	"context"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/remotes"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Provider provides necessary image utils, image content
// store for image conversion.
type Provider interface {
	// Use plain HTTP to communicate with registry.
	UsePlainHTTP()

	// Resolve attempts to resolve the reference into a name and descriptor.
	Resolver(ref string) (remotes.Resolver, error)
	// Pull pulls source image from remote registry by specified reference.
	// This pulls all platforms of the image but Image() returns containerd.Image for
	// the default platform.
	Pull(ctx context.Context, ref string) error
	// Push pushes target image to remote registry by specified reference,
	// the desc parameter represents the manifest of targe image.
	Push(ctx context.Context, desc ocispec.Descriptor, ref string) error

	// Image gets the source image descriptor.
	Image(ctx context.Context, ref string) (*ocispec.Descriptor, error)
	// ContentStore gets the content store object of containerd.
	ContentStore() content.Store
	// SetCacheRef sets the cache reference of the source image.
	SetCacheRef(ref string)
	// GetCacheRef gets the cache reference of the source image.
	GetCacheRef() string
}
