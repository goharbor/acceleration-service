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

package mount

import (
	"context"
	"fmt"
	"strings"

	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/mount"
	"github.com/containerd/containerd/snapshots"
	"github.com/pkg/errors"
)

func withMounts(ctx context.Context, chainID string, sn snapshots.Snapshotter, f func(mounts []mount.Mount) error) error {
	if chainID == "" {
		return f([]mount.Mount{})
	}
	var mounts []mount.Mount
	info, err := sn.Stat(ctx, chainID)
	if err != nil {
		return errors.Wrapf(err, "stat chain id %s", chainID)
	}
	if info.Kind == snapshots.KindActive {
		mounts, err = sn.Mounts(ctx, chainID)
		if err != nil {
			return errors.Wrapf(err, "get mounts by chain id %s", chainID)
		}
	} else {
		key := fmt.Sprintf("%s-view-key", chainID)
		mounts, err = sn.View(ctx, key, chainID)
		if err != nil {
			if errdefs.IsAlreadyExists(err) {
				mounts, err = sn.Mounts(ctx, key)
				if err != nil {
					return errors.Wrapf(err, "get mounts by key %s", key)
				}
				return f(mounts)
			}
			return errors.Wrapf(err, "view by key %s", key)
		}
		defer sn.Remove(ctx, key)
	}
	return f(mounts)
}

func parseUpper(mounts []mount.Mount) (string, error) {
	if len(mounts) == 0 {
		return "", errors.Errorf("invalid layer mounts")
	}

	if mounts[0].Type == "bind" {
		return mounts[0].Source, nil
	}

	// Parse overlayfs options to get topest upper directory path.
	if mounts[0].Type == "overlay" {
		var prefix = "lowerdir="
		for _, option := range mounts[0].Options {
			if strings.HasPrefix(option, prefix) {
				dirs := strings.Split(option[len(prefix):], ":")
				if len(dirs) > 0 {
					return dirs[0], nil
				}
			}
		}
		return "", errors.Errorf(
			"failed to parse overlay options %v",
			mounts[0].Options,
		)
	}

	return "", errors.Errorf(
		"unsupported mount type %q, only supports bind/overlay",
		mounts[0].Type,
	)
}

// Mount mounts a source image layer into a temp directory, provides
// to Nydus builder for converting the layer to Nydus format.
func Mount(
	ctx context.Context, sn snapshots.Snapshotter, lower, upper string, callback func(string, string, string) error,
) error {
	if err := withMounts(ctx, lower, sn, func(lowerMounts []mount.Mount) error {
		return withMounts(ctx, upper, sn, func(upperMounts []mount.Mount) error {
			return mount.WithTempMount(ctx, lowerMounts, func(lowerRoot string) error {
				return mount.WithTempMount(ctx, upperMounts, func(upperRoot string) error {
					upperSnapshot, err := parseUpper(upperMounts)
					if err != nil {
						return errors.Wrap(err, "parse upper snapshot")
					}
					return callback(lowerRoot, upperRoot, upperSnapshot)
				})
			})
		})
	}); err != nil {
		return errors.Wrapf(err, "mount layer, lower %s, upper %s", lower, upper)
	}
	return nil
}
