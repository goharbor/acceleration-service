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

package nydus

import (
	"context"
	"fmt"

	"github.com/pkg/errors"

	imageContent "github.com/containerd/containerd/content"
	"github.com/containerd/containerd/errdefs"
	imageMount "github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/snapshots"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/goharbor/acceleration-service/pkg/driver/nydus/backend"
	"github.com/goharbor/acceleration-service/pkg/driver/nydus/packer"
)

type buildLayer struct {
	chainID string
	sn      snapshots.Snapshotter
	cs      imageContent.Store
	backend backend.Backend
}

func (layer *buildLayer) Mount(ctx context.Context) ([]imageMount.Mount, func() error, error) {
	if layer.chainID == "" {
		return []imageMount.Mount{}, nil, nil
	}

	var mounts []imageMount.Mount
	info, err := layer.sn.Stat(ctx, layer.chainID)
	if err != nil {
		return nil, nil, errors.Wrapf(err, "stat chain id %s", layer.chainID)
	}

	if info.Kind == snapshots.KindActive {
		mounts, err = layer.sn.Mounts(ctx, layer.chainID)
		if err != nil {
			return nil, nil, errors.Wrapf(err, "get mounts by chain id %s", layer.chainID)
		}
		return mounts, func() error {
			return layer.sn.Remove(ctx, layer.chainID)
		}, nil
	}

	key := fmt.Sprintf("%s-view-key", layer.chainID)
	mounts, err = layer.sn.View(ctx, key, layer.chainID)
	if err != nil {
		if errdefs.IsAlreadyExists(err) {
			mounts, err = layer.sn.Mounts(ctx, key)
			if err != nil {
				return nil, nil, errors.Wrapf(err, "get mounts by key %s", key)
			}
			return mounts, func() error {
				return layer.sn.Remove(ctx, key)
			}, nil
		}
		return nil, nil, errors.Wrapf(err, "view by key %s", key)
	}

	return mounts, func() error {
		return layer.sn.Remove(ctx, key)
	}, nil
}

func (layer *buildLayer) ContentStore(ctx context.Context) imageContent.Store {
	return layer.cs
}

func (layer *buildLayer) GetCache(ctx context.Context, compressionType packer.CompressionType) (*ocispec.Descriptor, error) {
	// TODO: get cache from local storage.
	return nil, errdefs.ErrNotFound
}

func (layer *buildLayer) SetCache(ctx context.Context, compressionType packer.CompressionType, desc *ocispec.Descriptor) error {
	// TODO: set cache to local storage.
	return nil
}

func (layer *buildLayer) Backend(ctx context.Context) backend.Backend {
	return layer.backend
}
