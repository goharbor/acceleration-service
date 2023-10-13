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
	"io"
	"sync"

	"bytes"
	"encoding/json"

	ctrErrdefs "github.com/containerd/containerd/errdefs"
	"github.com/goharbor/acceleration-service/pkg/remote"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/remotes"
	nydusify "github.com/containerd/nydus-snapshotter/pkg/converter"
	nydusutils "github.com/goharbor/acceleration-service/pkg/driver/nydus/utils"
	"github.com/goharbor/acceleration-service/pkg/utils"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	"github.com/pkg/errors"
)

type cacheKey struct{}

type CacheItem struct {
	Source ocispec.Descriptor
	Target ocispec.Descriptor
}

type RemoteCache struct {
	mutex sync.Mutex
	// remoteCache is a map for caching target layer descriptors, the cache key is the source layer digest,
	// and the cache value is the target layer descriptor after conversion.
	cache map[digest.Digest]*CacheItem
	// cacheRef is the remote cache reference.
	cacheRef string
	// host is a func to provide registry credential by host name.
	host remote.HostFunc
	// cacheSize is the remote cache record capacity of converted layers.
	cacheSize int
}

func mergeMap(left, right map[string]string) map[string]string {
	if left == nil {
		left = map[string]string{}
	}
	if right == nil {
		right = map[string]string{}
	}
	for k, v := range right {
		left[k] = v
	}
	return left
}

func GetFromContext(ctx context.Context, dgst digest.Digest) (*RemoteCache, *ocispec.Descriptor) {
	rc, ok := ctx.Value(cacheKey{}).(*RemoteCache)
	if ok {
		return rc, rc.Get(dgst)
	}
	return nil, nil
}

func UpdateFromContext(ctx context.Context, dgst digest.Digest, labels map[string]string) (*RemoteCache, *ocispec.Descriptor) {
	rc, ok := ctx.Value(cacheKey{}).(*RemoteCache)
	if ok {
		if item := rc.getBySource(dgst); item != nil {
			item.Source.Annotations = mergeMap(item.Source.Annotations, labels)
			return rc, &item.Source
		}
		if item := rc.getByTarget(dgst); item != nil {
			item.Target.Annotations = mergeMap(item.Source.Annotations, labels)
			return rc, &item.Target
		}
	}
	return nil, nil
}

func SetFromContext(ctx context.Context, source, target ocispec.Descriptor) {
	rc, ok := ctx.Value(cacheKey{}).(*RemoteCache)
	if ok {
		rc.Set(source, target)
	}
}

func NewRemoteCache(ctx context.Context, cacheSize int, cacheRef string, host remote.HostFunc) (context.Context, *RemoteCache) {
	cache := &RemoteCache{
		cache:     make(map[digest.Digest]*CacheItem),
		host:      host,
		cacheSize: cacheSize,
		cacheRef:  cacheRef,
	}
	cxt := context.WithValue(ctx, cacheKey{}, cache)
	return cxt, cache
}

func (rc *RemoteCache) getByTarget(target digest.Digest) *CacheItem {
	for _, item := range rc.cache {
		if item.Target.Digest == target {
			return item
		}
	}
	return nil
}

func (rc *RemoteCache) getBySource(source digest.Digest) *CacheItem {
	return rc.cache[source]
}

func (rc *RemoteCache) Get(digest digest.Digest) *ocispec.Descriptor {
	rc.mutex.Lock()
	defer rc.mutex.Unlock()
	if item := rc.getBySource(digest); item != nil {
		return &item.Source
	}
	if item := rc.getByTarget(digest); item != nil {
		return &item.Target
	}
	return nil
}

func (rc *RemoteCache) Set(source, target ocispec.Descriptor) {
	rc.mutex.Lock()
	defer rc.mutex.Unlock()
	rc.cache[source.Digest] = &CacheItem{
		Source: source,
		Target: target,
	}
}

// Fetch fetch cache manifest from remote
func (rc *RemoteCache) Fetch(ctx context.Context, pvd Provider, platformMC platforms.MatchComparer) (*ocispec.Descriptor, error) {
	resolver, err := pvd.Resolver(rc.cacheRef)
	if err != nil {
		return nil, err
	}

	remoteContext := &containerd.RemoteContext{
		Resolver:        resolver,
		PlatformMatcher: platformMC,
	}
	name, desc, err := remoteContext.Resolver.Resolve(ctx, rc.cacheRef)
	if err != nil {
		return nil, err
	}
	fetcher, err := remoteContext.Resolver.Fetcher(ctx, name)
	if err != nil {
		return nil, err
	}
	ir, err := fetcher.Fetch(ctx, desc)
	if err != nil {
		return nil, errors.Wrap(err, "pull remote cache")
	}
	defer ir.Close()
	mBytes, err := io.ReadAll(ir)
	if err != nil {
		return nil, errors.Wrap(err, "read remote cache bytes to manifest index")
	}

	cs := pvd.ContentStore()
	switch desc.MediaType {
	case images.MediaTypeDockerSchema2ManifestList, ocispec.MediaTypeImageIndex:
		manifestIndex := ocispec.Index{}
		if err = json.Unmarshal(mBytes, &manifestIndex); err != nil {
			return nil, err
		}
		manifestIndexDesc, _, err := nydusutils.MarshalToDesc(manifestIndex, ocispec.MediaTypeImageIndex)
		if err != nil {
			return nil, errors.Wrap(err, "marshal remote cache manifest index")
		}
		if err = content.WriteBlob(ctx, cs, rc.cacheRef, bytes.NewReader(mBytes), *manifestIndexDesc); err != nil {
			return nil, errors.Wrap(err, "write remote cache manifest index")
		}
		for _, manifest := range manifestIndex.Manifests {
			mDesc := ocispec.Descriptor{
				MediaType: manifest.MediaType,
				Digest:    manifest.Digest,
				Size:      manifest.Size,
			}
			mir, err := fetcher.Fetch(ctx, mDesc)
			if err != nil {
				return nil, errors.Wrap(err, "fetch remote cache")
			}
			manifestBytes, err := io.ReadAll(mir)
			if err != nil {
				return nil, errors.Wrap(err, "read remote cache manifest")
			}
			if err = content.WriteBlob(ctx, cs, rc.cacheRef, bytes.NewReader(manifestBytes), mDesc); err != nil {
				return nil, errors.Wrap(err, "write remote cache manifest")
			}
		}

		// Get manifests which matches specified platforms and put them into lru cache
		matchDescs, err := utils.GetManifests(ctx, cs, *manifestIndexDesc, platformMC)
		if err != nil {
			return nil, errors.Wrap(err, "get remote cache manifest list")
		}
		var targetManifests []ocispec.Manifest
		for _, desc := range matchDescs {
			targetManifest := ocispec.Manifest{}
			_, err = utils.ReadJSON(ctx, cs, &targetManifest, desc)
			if err != nil {
				return nil, errors.Wrap(err, "read remote cache manifest")
			}
			targetManifests = append(targetManifests, targetManifest)

		}
		for _, manifest := range targetManifests {
			for _, targetDesc := range manifest.Layers {
				sourceDigest := digest.Digest(targetDesc.Annotations[nydusify.LayerAnnotationNydusSourceDigest])
				if sourceDigest.Validate() != nil {
					continue
				}
				reader, sourceDesc, err := fetcher.(remotes.FetcherByDigest).FetchByDigest(ctx, sourceDigest)
				if err != nil {
					return nil, err
				}
				reader.Close()
				if sourceDesc.Annotations == nil {
					sourceDesc.Annotations = map[string]string{}
				}
				sourceDesc.Annotations[nydusify.LayerAnnotationNydusTargetDigest] = string(targetDesc.Digest)
				if targetDesc.Annotations == nil {
					targetDesc.Annotations = map[string]string{}
				}
				targetDesc.Annotations[nydusify.LayerAnnotationUncompressed] = string(targetDesc.Digest)
				rc.Set(sourceDesc, targetDesc)
			}
		}
		return manifestIndexDesc, nil
	default:
		return nil, fmt.Errorf("unsupported cache image mediatype %s", desc.MediaType)
	}
}

// push cache manifest to remote
func (rc *RemoteCache) push(ctx context.Context, pvd Provider, cacheIndex *ocispec.Index) error {
	for _, manifest := range cacheIndex.Manifests {
		if err := pvd.Push(ctx, manifest, rc.cacheRef); err != nil {
			return err
		}
	}
	manifestIndexDesc, manifestIndexBytes, err := nydusutils.MarshalToDesc(*cacheIndex, ocispec.MediaTypeImageIndex)
	if err != nil {
		return errors.Wrap(err, "marshal remote cache manifest index")
	}
	if err = content.WriteBlob(ctx, pvd.ContentStore(), rc.cacheRef, bytes.NewReader(manifestIndexBytes), *manifestIndexDesc); err != nil {
		return errors.Wrap(err, "write remote cache manifest index")
	}
	return pvd.Push(ctx, *manifestIndexDesc, rc.cacheRef)
}

// update cache layer from upper to lower, and generate corresponding cache manifest with converted image descriptor
func (rc *RemoteCache) update(ctx context.Context, provider Provider, orgDesc, newDesc, cacheDesc *ocispec.Descriptor,
	platformMC platforms.MatchComparer) (*ocispec.Index, error) {
	cs := provider.ContentStore()
	cacheLayers := map[*platforms.Platform][]ocispec.Descriptor{}

	switch orgDesc.MediaType {
	case ocispec.MediaTypeImageManifest, images.MediaTypeDockerSchema2Manifest:
		targetLayers, err := rc.getTargetLayers(ctx, cs, *orgDesc)
		if err != nil {
			return nil, err
		}
		// platform of original or new image maybe lost, get from config platform
		platform, err := images.Platforms(ctx, cs, *orgDesc)
		if err != nil {
			return nil, err
		}
		cacheLayers[&platform[0]] = targetLayers

	case images.MediaTypeDockerSchema2ManifestList, ocispec.MediaTypeImageIndex:
		orgManifests, err := utils.GetManifests(ctx, cs, *orgDesc, platformMC)
		if err != nil {
			return nil, errors.Wrap(err, "get original manifest list")
		}
		newManifests, err := utils.GetManifests(ctx, cs, *newDesc, platformMC)
		if err != nil {
			return nil, errors.Wrap(err, "get new manifest list")
		}
		for _, newManifestDesc := range newManifests {
			targetLayers := []ocispec.Descriptor{}
			newManiPlatforms, err := images.Platforms(ctx, cs, newManifestDesc)
			if err != nil {
				return nil, errors.Wrap(err, "get converted manifest platforms")
			}
			// find original manifest matches converted manifest's platform
			matcher := platforms.NewMatcher(newManiPlatforms[0])
			for _, orgManifestDesc := range orgManifests {
				orgManiPlatforms, err := images.Platforms(ctx, cs, orgManifestDesc)
				if err != nil {
					return nil, errors.Wrap(err, "get original manifest platforms")
				}

				if matcher.Match(orgManiPlatforms[0]) {
					targetLayers, err = rc.getTargetLayers(ctx, cs, orgManifestDesc)
					if err != nil {
						return nil, err
					}
					break
				}
			}
			cacheLayers[newManifestDesc.Platform] = targetLayers
		}
	}

	imageConfig := ocispec.ImageConfig{}
	imageConfigDesc, imageConfigBytes, err := nydusutils.MarshalToDesc(imageConfig, ocispec.MediaTypeImageConfig)
	if err != nil {
		return nil, errors.Wrap(err, "marshal remote cache image config")
	}
	if err = content.WriteBlob(ctx, cs, rc.cacheRef, bytes.NewReader(imageConfigBytes), *imageConfigDesc); err != nil {
		return nil, errors.Wrap(err, "write remote cahce image config")
	}

	cacheIndex := ocispec.Index{
		Versioned: specs.Versioned{
			SchemaVersion: 2,
		},
		MediaType: ocispec.MediaTypeImageIndex,
		Manifests: []ocispec.Descriptor{},
	}
	// cacheDesc maybe nil if remote cache doesn't exists before
	if cacheDesc != nil {
		_, err = utils.ReadJSON(ctx, cs, &cacheIndex, *cacheDesc)
		if err != nil {
			return nil, errors.Wrap(err, "read cache manifest index")
		}
		for idx, maniDesc := range cacheIndex.Manifests {
			matcher := platforms.NewMatcher(*maniDesc.Platform)
			for platform, layers := range cacheLayers {
				if matcher.Match(*platform) {
					// append new cache layers to existed cache manifest
					var manifest ocispec.Manifest
					_, err = utils.ReadJSON(ctx, cs, &manifest, maniDesc)
					if err != nil {
						return nil, errors.Wrap(err, "read cache manifest")
					}
					manifest.Layers = appendLayers(manifest.Layers, layers, rc.cacheSize)
					newManiDesc, err := utils.WriteJSON(ctx, cs, manifest, maniDesc, "", nil)
					if err != nil {
						return nil, errors.Wrap(err, "write cache manifest")
					}
					cacheIndex.Manifests[idx] = *newManiDesc
					delete(cacheLayers, platform)
				}
			}
		}
	}

	// append new cache layers to new cache manifest
	for platform, layers := range cacheLayers {
		manifest := ocispec.Manifest{
			Versioned: specs.Versioned{
				SchemaVersion: 2,
			},
			MediaType: ocispec.MediaTypeImageManifest,
			Config:    *imageConfigDesc,
			Layers:    layers,
		}
		manifestDesc, err := utils.WriteJSON(ctx, cs, manifest, ocispec.Descriptor{}, "", nil)
		if err != nil {
			return nil, errors.Wrap(err, "write cache manifest")
		}
		cacheIndex.Manifests = append(cacheIndex.Manifests, ocispec.Descriptor{
			MediaType: ocispec.MediaTypeImageManifest,
			Digest:    manifestDesc.Digest,
			Size:      manifestDesc.Size,
			Platform:  platform,
		})
	}
	return &cacheIndex, nil
}

// UpdateAndPush updates cache layers from current conversion and push cache manifest to remote.
func (rc *RemoteCache) UpdateAndPush(ctx context.Context, provider Provider, orgDesc, newDesc *ocispec.Descriptor, platformMC platforms.MatchComparer) error {
	// Fetch the old remote cache before updating and pushing the new one to avoid conflict.
	cacheDesc, err := rc.Fetch(ctx, provider, platformMC)
	if err != nil && !errors.Is(err, ctrErrdefs.ErrNotFound) {
		return err
	}
	cacheIndex, err := rc.update(ctx, provider, orgDesc, newDesc, cacheDesc, platformMC)
	if err != nil {
		return err
	}
	return rc.push(ctx, provider, cacheIndex)
}

// getTargetLayers get cached target layers
func (rc *RemoteCache) getTargetLayers(ctx context.Context, cs content.Store, sourceManiDesc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
	sourceManifest := ocispec.Manifest{}
	_, err := utils.ReadJSON(ctx, cs, &sourceManifest, sourceManiDesc)
	if err != nil {
		return nil, errors.Wrap(err, "read original manifest json")
	}

	targetLayers := []ocispec.Descriptor{}

	for _, sourceLayer := range sourceManifest.Layers {
		if item := rc.getBySource(sourceLayer.Digest); item != nil {
			targetLayers = append(targetLayers, item.Target)
		}
	}

	return targetLayers, nil
}

// appendLayersappend new cache layers to cache manifest layers, if new layer already exists, moving existed layers to front, avoiding to add duplicated layers.
func appendLayers(orgDescs, newDescs []ocispec.Descriptor, size int) []ocispec.Descriptor {
	moveFront := map[digest.Digest]bool{}
	for _, desc := range orgDescs {
		moveFront[desc.Digest] = true
	}
	mergedLayers := orgDescs
	for _, desc := range newDescs {
		if !moveFront[desc.Digest] {
			mergedLayers = append(mergedLayers, desc)
			if len(mergedLayers) >= size {
				break
			}
		}
	}
	if len(mergedLayers) > size {
		mergedLayers = mergedLayers[:size]
	}
	return mergedLayers
}
