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

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/remotes"
	"github.com/goharbor/acceleration-service/pkg/remote"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

var logger = logrus.WithField("module", "content")

type LocalProvider struct {
	usePlainHTTP bool
	client       *containerd.Client
	hosts        remote.HostFunc
	platformMC   platforms.MatchComparer
}

func NewLocalProvider(
	client *containerd.Client,
	hosts remote.HostFunc,
	platformMC platforms.MatchComparer,
) (Provider, error) {
	return &LocalProvider{
		client:     client,
		hosts:      hosts,
		platformMC: platformMC,
	}, nil
}

func (pvd *LocalProvider) UsePlainHTTP() {
	pvd.usePlainHTTP = true
}

func (pvd *LocalProvider) Resolver(ref string) (remotes.Resolver, error) {
	credFunc, insecure, err := pvd.hosts(ref)
	if err != nil {
		return nil, err
	}
	return remote.NewResolver(insecure, pvd.usePlainHTTP, credFunc), nil
}

func (pvd *LocalProvider) Pull(ctx context.Context, ref string) error {
	resolver, err := pvd.Resolver(ref)
	if err != nil {
		return err
	}

	opts := []containerd.RemoteOpt{
		// TODO: sets max concurrent downloaded layer limit by containerd.WithMaxConcurrentDownloads.
		containerd.WithPlatformMatcher(pvd.platformMC),
		containerd.WithImageHandler(images.HandlerFunc(
			func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
				if images.IsLayerType(desc.MediaType) {
					logger.Debugf("pulling layer %s", desc.Digest)
				}
				return nil, nil
			},
		)),
		containerd.WithResolver(resolver),
	}

	// Pull the source image from remote registry.
	_, err = pvd.client.Fetch(ctx, ref, opts...)
	if err != nil {
		return errors.Wrap(err, "pull source image")
	}

	return nil
}

func (pvd *LocalProvider) Push(ctx context.Context, desc ocispec.Descriptor, ref string) error {
	resolver, err := pvd.Resolver(ref)
	if err != nil {
		return err
	}

	opts := []containerd.RemoteOpt{
		containerd.WithResolver(resolver),
		containerd.WithPlatformMatcher(pvd.platformMC),
	}

	// TODO: sets max concurrent uploaded layer limit by containerd.WithMaxConcurrentUploadedLayers.
	return pvd.client.Push(ctx, ref, desc, opts...)
}

func (pvd *LocalProvider) Image(ctx context.Context, ref string) (*ocispec.Descriptor, error) {
	image, err := pvd.client.GetImage(ctx, ref)
	if err != nil {
		return nil, err
	}
	target := image.Target()
	return &target, nil
}

func (pvd *LocalProvider) ContentStore() content.Store {
	return pvd.client.ContentStore()
}
