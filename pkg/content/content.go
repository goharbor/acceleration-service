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
	"fmt"
	"io"
	"net/url"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/remotes"
	"github.com/containerd/containerd/snapshots"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/goharbor/acceleration-service/pkg/config"
	"github.com/goharbor/acceleration-service/pkg/remote"
)

var logger = logrus.WithField("module", "content")

// Provider provides necessary image utils, image content
// store for image conversion.
type Provider interface {
	// Pull pulls source image from remote registry by specified reference.
	Pull(ctx context.Context, ref string) error
	// Push pushes target image to remote registry by specified reference,
	// the desc parameter represents the manifest of targe image.
	Push(ctx context.Context, desc ocispec.Descriptor, ref string) error

	// Image gets the source image object.
	Image() containerd.Image
	// Snapshotter gets the snapshotter object of containerd.
	Snapshotter() snapshots.Snapshotter
	// ContentStore gets the content store object of containerd.
	ContentStore() content.Store
}

type LocalProvider struct {
	image       containerd.Image
	cfg         *config.ProviderConfig
	snapshotter snapshots.Snapshotter
	client      *containerd.Client
}

func NewLocalProvider(
	cfg *config.ProviderConfig, client *containerd.Client, snapshotter snapshots.Snapshotter,
) (Provider, error) {
	return &LocalProvider{
		cfg:         cfg,
		snapshotter: snapshotter,
		client:      client,
	}, nil
}

func (pvd *LocalProvider) getResolver(ctx context.Context, ref string) (remotes.Resolver, error) {
	refURL, err := url.Parse(fmt.Sprintf("dummy://%s", ref))
	if err != nil {
		return nil, errors.Wrap(err, "parse reference of source image")
	}

	auth, ok := pvd.cfg.Source[refURL.Host]
	if !ok {
		return nil, fmt.Errorf("not found matched hostname %s in config", refURL.Host)
	}

	resolver := remote.NewResolver(auth.Insecure, remote.NewBasicAuthCredFunc(auth.Auth))

	return resolver, nil
}

func (pvd *LocalProvider) Pull(ctx context.Context, ref string) error {
	resolver, err := pvd.getResolver(ctx, ref)
	if err != nil {
		return errors.Wrapf(err, "get resolver for %s", ref)
	}

	opts := []containerd.RemoteOpt{
		// TODO: support multi-platform source images.
		// TODO: sets max concurrent downloaded layer limit by containerd.WithMaxConcurrentDownloads.
		containerd.WithImageHandler(images.HandlerFunc(
			func(ctx context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
				if images.IsLayerType(desc.MediaType) {
					logger.Debugf("pulling layer %s", desc.Digest)
				}
				return nil, nil
			},
		)),
		containerd.WithPullUnpack,
		containerd.WithResolver(resolver),
	}

	// Pull the source image from remote registry.
	image, err := pvd.client.Pull(ctx, ref, opts...)
	if err != nil {
		return errors.Wrap(err, "pull source image")
	}
	pvd.image = image

	return nil
}

func (pvd *LocalProvider) Write(ctx context.Context, desc ocispec.Descriptor, reader io.Reader, labels map[string]string) error {
	digest := desc.Digest.String()

	if err := content.WriteBlob(
		ctx, pvd.client.ContentStore(), digest, reader, desc, content.WithLabels(labels),
	); err != nil {
		return errors.Wrapf(err, "write blob %s", digest)
	}

	return nil
}

func (pvd *LocalProvider) Read(ctx context.Context, desc ocispec.Descriptor) ([]byte, error) {
	return content.ReadBlob(ctx, pvd.client.ContentStore(), desc)
}

func (pvd *LocalProvider) Push(ctx context.Context, desc ocispec.Descriptor, ref string) error {
	resolver, err := pvd.getResolver(ctx, ref)
	if err != nil {
		return errors.Wrapf(err, "get resolver for %s", ref)
	}

	// TODO: sets max concurrent uploaded layer limit by containerd.WithMaxConcurrentUploadedLayers.
	return pvd.client.Push(ctx, ref, desc, containerd.WithResolver(resolver))
}

func (pvd *LocalProvider) Image() containerd.Image {
	return pvd.image
}

func (pvd *LocalProvider) Snapshotter() snapshots.Snapshotter {
	return pvd.snapshotter
}

func (pvd *LocalProvider) ContentStore() content.Store {
	return pvd.client.ContentStore()
}
