// Copyright Project Harbor Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
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
	"strings"
	"sync"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/content/local"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/log"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/remotes"
	"github.com/containerd/containerd/remotes/docker"

	// nolint:staticcheck
	"github.com/containerd/containerd/remotes/docker/schema1"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"golang.org/x/sync/semaphore"
	"golang.org/x/sync/singleflight"
)

var fetchSingleflight = &singleflight.Group{}

// Ported from containerd project, copyright The containerd Authors.
// github.com/containerd/containerd/blob/main/pull.go
func fetch(ctx context.Context, store content.Store, rCtx *containerd.RemoteContext, ref string, limit int) (images.Image, error) {
	name, desc, err := rCtx.Resolver.Resolve(ctx, ref)
	if err != nil {
		return images.Image{}, fmt.Errorf("failed to resolve reference %q: %w", ref, err)
	}

	fetcher, err := rCtx.Resolver.Fetcher(ctx, name)
	if err != nil {
		return images.Image{}, fmt.Errorf("failed to get fetcher for %q: %w", name, err)
	}

	var (
		handler images.Handler

		isConvertible bool
		converterFunc func(context.Context, ocispec.Descriptor) (ocispec.Descriptor, error)
		limiter       *semaphore.Weighted
	)

	// nolint:staticcheck
	if desc.MediaType == images.MediaTypeDockerSchema1Manifest && rCtx.ConvertSchema1 {
		schema1Converter := schema1.NewConverter(store, fetcher)

		handler = images.Handlers(append(rCtx.BaseHandlers, schema1Converter)...)

		isConvertible = true

		converterFunc = func(ctx context.Context, _ ocispec.Descriptor) (ocispec.Descriptor, error) {
			return schema1Converter.Convert(ctx)
		}
	} else {
		// Get all the children for a descriptor
		childrenHandler := images.ChildrenHandler(store)
		// Set any children labels for that content
		childrenHandler = images.SetChildrenMappedLabels(store, childrenHandler, rCtx.ChildLabelMap)
		if rCtx.AllMetadata {
			// Filter manifests by platforms but allow to handle manifest
			// and configuration for not-target platforms
			childrenHandler = remotes.FilterManifestByPlatformHandler(childrenHandler, rCtx.PlatformMatcher)
		} else {
			// Filter children by platforms if specified.
			childrenHandler = images.FilterPlatforms(childrenHandler, rCtx.PlatformMatcher)
		}
		// Sort and limit manifests if a finite number is needed
		if limit > 0 {
			childrenHandler = images.LimitManifests(childrenHandler, rCtx.PlatformMatcher, limit)
		}

		// set isConvertible to true if there is application/octet-stream media type
		convertibleHandler := images.HandlerFunc(
			func(_ context.Context, desc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
				if desc.MediaType == docker.LegacyConfigMediaType {
					isConvertible = true
				}

				return []ocispec.Descriptor{}, nil
			},
		)

		appendDistSrcLabelHandler, err := docker.AppendDistributionSourceLabel(store, ref)
		if err != nil {
			return images.Image{}, err
		}

		handlers := append(rCtx.BaseHandlers,
			fetchHandler(store, fetcher),
			convertibleHandler,
			childrenHandler,
			appendDistSrcLabelHandler,
		)

		handler = images.Handlers(handlers...)

		converterFunc = func(ctx context.Context, desc ocispec.Descriptor) (ocispec.Descriptor, error) {
			return docker.ConvertManifest(ctx, store, desc)
		}
	}

	if rCtx.HandlerWrapper != nil {
		handler = rCtx.HandlerWrapper(handler)
	}

	if rCtx.MaxConcurrentDownloads > 0 {
		limiter = semaphore.NewWeighted(int64(rCtx.MaxConcurrentDownloads))
	}

	if err := images.Dispatch(ctx, handler, limiter, desc); err != nil {
		return images.Image{}, err
	}

	if isConvertible {
		if desc, err = converterFunc(ctx, desc); err != nil {
			return images.Image{}, err
		}
	}

	return images.Image{
		Name:   name,
		Target: desc,
		Labels: rCtx.Labels,
	}, nil
}

// Ported from containerd project, copyright The containerd Authors.
// https://github.com/containerd/containerd/blob/main/remotes/handlers.go
func fetchHandler(ingester content.Ingester, fetcher remotes.Fetcher) images.HandlerFunc {
	return func(ctx context.Context, desc ocispec.Descriptor) (subdescs []ocispec.Descriptor, err error) {
		ctx = log.WithLogger(ctx, log.G(ctx).WithFields(log.Fields{ // nolint:staticcheck
			"digest":    desc.Digest,
			"mediatype": desc.MediaType,
			"size":      desc.Size,
		}))

		switch desc.MediaType {
		case images.MediaTypeDockerSchema1Manifest:
			return nil, fmt.Errorf("%v not supported", desc.MediaType)
		default:
			_, err, _ := fetchSingleflight.Do(string(desc.Digest), func() (interface{}, error) {
				return nil, remotes.Fetch(ctx, ingester, fetcher, desc)
			})
			if errdefs.IsAlreadyExists(err) {
				return nil, nil
			}
			return nil, err
		}
	}
}

// Ported from containerd project, copyright The containerd Authors.
// github.com/containerd/containerd/blob/main/client.go
func push(ctx context.Context, store content.Store, pushCtx *containerd.RemoteContext, desc ocispec.Descriptor, ref string) error {
	if pushCtx.PlatformMatcher == nil {
		if len(pushCtx.Platforms) > 0 {
			var ps []ocispec.Platform
			for _, platform := range pushCtx.Platforms {
				p, err := platforms.Parse(platform)
				if err != nil {
					return fmt.Errorf("invalid platform %s: %w", platform, err)
				}
				ps = append(ps, p)
			}
			pushCtx.PlatformMatcher = platforms.Any(ps...)
		} else {
			pushCtx.PlatformMatcher = platforms.All
		}
	}

	// Annotate ref with digest to push only push tag for single digest
	if !strings.Contains(ref, "@") {
		ref = ref + "@" + desc.Digest.String()
	}

	pusher, err := pushCtx.Resolver.Pusher(ctx, ref)
	if err != nil {
		return err
	}

	var wrapper func(images.Handler) images.Handler

	if len(pushCtx.BaseHandlers) > 0 {
		wrapper = func(h images.Handler) images.Handler {
			h = images.Handlers(append(pushCtx.BaseHandlers, h)...)
			if pushCtx.HandlerWrapper != nil {
				h = pushCtx.HandlerWrapper(h)
			}
			return h
		}
	} else if pushCtx.HandlerWrapper != nil {
		wrapper = pushCtx.HandlerWrapper
	}

	var limiter *semaphore.Weighted
	if pushCtx.MaxConcurrentUploadedLayers > 0 {
		limiter = semaphore.NewWeighted(int64(pushCtx.MaxConcurrentUploadedLayers))
	}

	return remotes.PushContent(ctx, pusher, desc, store, limiter, pushCtx.PlatformMatcher, wrapper)
}

// Ported from containerd project, copyright The containerd Authors.
// https://github.com/containerd/containerd/blob/main/content/local/store_test.go
type memoryLabelStore struct {
	l      sync.Mutex
	labels map[digest.Digest]map[string]string
}

func newMemoryLabelStore() local.LabelStore {
	return &memoryLabelStore{
		labels: map[digest.Digest]map[string]string{},
	}
}

func (mls *memoryLabelStore) Get(d digest.Digest) (map[string]string, error) {
	mls.l.Lock()
	labels := mls.labels[d]
	mls.l.Unlock()

	return labels, nil
}

func (mls *memoryLabelStore) Set(d digest.Digest, labels map[string]string) error {
	mls.l.Lock()
	mls.labels[d] = labels
	mls.l.Unlock()

	return nil
}

func (mls *memoryLabelStore) Update(d digest.Digest, update map[string]string) (map[string]string, error) {
	mls.l.Lock()
	labels, ok := mls.labels[d]
	if !ok {
		labels = map[string]string{}
	}
	for k, v := range update {
		if v == "" {
			delete(labels, k)
		} else {
			labels[k] = v
		}
	}
	mls.labels[d] = labels
	mls.l.Unlock()

	return labels, nil
}
