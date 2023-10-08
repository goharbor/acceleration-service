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
	"math"
	"sort"
	"strconv"
	"sync"

	"bytes"
	"encoding/json"

	"github.com/containerd/containerd/leases"
	"github.com/containerd/containerd/namespaces"
	"github.com/goharbor/acceleration-service/pkg/remote"
	lru "github.com/hashicorp/golang-lru/v2"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/content"
	containerdErrDefs "github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/platforms"
	nydusutils "github.com/goharbor/acceleration-service/pkg/driver/nydus/utils"
	"github.com/goharbor/acceleration-service/pkg/errdefs"
	"github.com/goharbor/acceleration-service/pkg/utils"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	"github.com/pkg/errors"
)

// This is not thread-safe, which means it will depend on the parent implementation to do the locking mechanism.
type leaseCache struct {
	caches      map[string]*lru.Cache[string, any]
	cachesIndex []int
	size        int
}

// newleaseCache return new empty leaseCache
func newLeaseCache() *leaseCache {
	return &leaseCache{
		caches:      make(map[string]*lru.Cache[string, any]),
		cachesIndex: make([]int, 0),
		size:        0,
	}
}

// Init leaseCache by leases manager from db
func (leaseCache *leaseCache) Init(lm leases.Manager) error {
	ls, err := lm.List(namespaces.WithNamespace(context.Background(), accelerationServiceNamespace))
	if err != nil {
		return err
	}
	sort.Slice(ls, func(i, j int) bool {
		return ls[i].Labels[usedAtLabel] > ls[j].Labels[usedAtLabel]
	})
	for _, lease := range ls {
		if err := leaseCache.Add(lease.ID, lease.Labels[usedCountLabel]); err != nil {
			return err
		}
	}
	return nil
}

// Add the key into cache
func (leaseCache *leaseCache) Add(key string, usedCount string) error {
	count, err := strconv.Atoi(usedCount)
	if err != nil {
		return err
	}
	if cache, ok := leaseCache.caches[usedCount]; ok {
		cache.Add(key, nil)
	} else {
		cache, err := lru.New[string, any](math.MaxInt)
		if err != nil {
			return err
		}
		cache.Add(key, nil)
		leaseCache.caches[usedCount] = cache
		usedCount, err := strconv.Atoi(usedCount)
		if err != nil {
			return err
		}
		leaseCache.cachesIndex = append(leaseCache.cachesIndex, usedCount)
		sort.Ints(leaseCache.cachesIndex)
	}
	// remove old cache
	if usedCount != "1" {
		if cache, ok := leaseCache.caches[strconv.Itoa(count-1)]; ok {
			if cache.Contains(key) {
				leaseCache.remove(key, strconv.Itoa(count-1))
				leaseCache.size--
			}
		}
	}
	leaseCache.size++
	return nil
}

// Remove oldest key from cache
func (leaseCache *leaseCache) Remove() (string, error) {
	if key, _, ok := leaseCache.caches[strconv.Itoa(leaseCache.cachesIndex[0])].GetOldest(); ok {
		leaseCache.remove(key, strconv.Itoa(leaseCache.cachesIndex[0]))
		leaseCache.size--
		return key, nil
	}
	return "", fmt.Errorf("leaseCache have empty cache with cachesIndex")
}

func (leaseCache *leaseCache) remove(key string, usedCount string) {
	leaseCache.caches[usedCount].Remove(key)
	if leaseCache.caches[usedCount].Len() == 0 {
		delete(leaseCache.caches, usedCount)
		var newCachesIndex []int
		for _, index := range leaseCache.cachesIndex {
			if usedCount != strconv.Itoa(index) {
				newCachesIndex = append(newCachesIndex, index)
			}
		}
		leaseCache.cachesIndex = newCachesIndex
	}
}

// Len return the size of leaseCache
func (leaseCache *leaseCache) Len() int {
	return leaseCache.size
}

type key int

const (
	Cache key = iota
)

type RemoteCache struct {
	mutex sync.Mutex
	// remoteCache is a map for caching target layer descriptors, the cache key is the source layer digest,
	// and the cache value is the target layer descriptor after conversion.
	remoteCache map[string]ocispec.Descriptor
	// cacheRef is the remote cache reference.
	cacheRef string
	// host is a func to provide registry credential by host name.
	host remote.HostFunc
	// cacheSize is the remote cache record capacity of converted layers.
	cacheSize int
}

func NewRemoteCache(cacheSize int, cacheRef string, host remote.HostFunc) (*RemoteCache, error) {
	return &RemoteCache{
		remoteCache: make(map[string]ocispec.Descriptor),
		host:        host,
		cacheSize:   cacheSize,
		cacheRef:    cacheRef,
	}, nil
}

func (rc *RemoteCache) Values() []ocispec.Descriptor {
	rc.mutex.Lock()
	defer rc.mutex.Unlock()
	var values []ocispec.Descriptor
	for _, desc := range rc.remoteCache {
		values = append(values, desc)
	}
	return values
}

func (rc *RemoteCache) Get(key string) (ocispec.Descriptor, bool) {
	rc.mutex.Lock()
	defer rc.mutex.Unlock()
	value, ok := rc.remoteCache[key]
	return value, ok
}

func (rc *RemoteCache) Add(key string, value ocispec.Descriptor) {
	rc.mutex.Lock()
	defer rc.mutex.Unlock()
	rc.remoteCache[key] = value
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
		if errors.Is(err, containerdErrDefs.ErrNotFound) {
			// Remote cache may do not exist, just return nil
			return nil, nil
		}
		return nil, err
	}
	fetcher, err := remoteContext.Resolver.Fetcher(ctx, name)
	if err != nil {
		return nil, err
	}
	ir, err := fetcher.Fetch(ctx, desc)
	if err != nil {
		if errdefs.NeedsRetryWithHTTP(err) {
			pvd.UsePlainHTTP()
			ir, err = fetcher.Fetch(ctx, desc)
			if err != nil {
				return nil, errors.Wrap(err, "try to pull remote cache")
			}
		} else {
			return nil, errors.Wrap(err, "pull remote cache")
		}
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
			for _, layer := range manifest.Layers {
				sourceDesc, ok := layer.Annotations[nydusutils.LayerAnnotationNydusSourceDigest]
				if ok {
					rc.Add(sourceDesc, layer)
				}
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
		targetLayers, err := getConvertedLayers(ctx, cs, *orgDesc, *newDesc)
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
					targetLayers, err = getConvertedLayers(ctx, cs, orgManifestDesc, newManifestDesc)
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
	if err != nil {
		return err
	}
	cacheIndex, err := rc.update(ctx, provider, orgDesc, newDesc, cacheDesc, platformMC)
	if err != nil {
		return err
	}
	return rc.push(ctx, provider, cacheIndex)
}

// getConvertedLayers get converted layers of nydus image and corresponding source image layers from the descriptor
// from nydus image and source image
func getConvertedLayers(ctx context.Context, cs content.Store, sourceManiDesc, targetManiDesc ocispec.Descriptor) ([]ocispec.Descriptor, error) {
	sourceManifest := ocispec.Manifest{}
	_, err := utils.ReadJSON(ctx, cs, &sourceManifest, sourceManiDesc)
	if err != nil {
		return nil, errors.Wrap(err, "read original manifest json")
	}

	targetManifest := ocispec.Manifest{}
	_, err = utils.ReadJSON(ctx, cs, &targetManifest, targetManiDesc)
	if err != nil {
		return nil, errors.Wrap(err, "read new manifest json")
	}
	// the final layer of Layers is boostrap layer of nydus image, it doesn't have corresponding source image layer
	targetLayers := targetManifest.Layers[:len(targetManifest.Layers)-1]

	// Update cache to cacheLayers from upper to lower and update layer laebl
	cacheLayers := []ocispec.Descriptor{}
	for i := len(targetLayers) - 1; i >= 0; i-- {
		layer := targetLayers[i]
		// Update <LayerAnnotationNydusSourceDigest> label for each layer
		layer.Annotations[nydusutils.LayerAnnotationNydusSourceDigest] = sourceManifest.Layers[i].Digest.String()
		cacheLayers = append(cacheLayers, layer)
	}

	return cacheLayers, nil
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
