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
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/remotes"
	nydusErrdefs "github.com/goharbor/acceleration-service/pkg/errdefs"
	"github.com/goharbor/acceleration-service/pkg/remote"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/specs-go"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

type LocalProvider struct {
	mutex        sync.Mutex
	images       map[string]*ocispec.Descriptor
	usePlainHTTP bool
	content      *Content
	hosts        remote.HostFunc
	platformMC   platforms.MatchComparer
	remoteCache  *Cache
}

func NewLocalProvider(
	workDir string,
	threshold string,
	hosts remote.HostFunc,
	platformMC platforms.MatchComparer,
) (Provider, *Content, error) {
	contentDir := filepath.Join(workDir, "content")
	if err := os.MkdirAll(contentDir, 0755); err != nil {
		return nil, nil, errors.Wrap(err, "create local provider work directory")
	}
	content, err := NewContent(contentDir, workDir, threshold)
	if err != nil {
		return nil, nil, errors.Wrap(err, "create local provider content")
	}
	return &LocalProvider{
		content:     content,
		images:      make(map[string]*ocispec.Descriptor),
		hosts:       hosts,
		platformMC:  platformMC,
		remoteCache: NewCache(100),
	}, content, nil
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

	// TODO: sets max concurrent downloaded layer limit by containerd.WithMaxConcurrentDownloads.
	rc := &containerd.RemoteContext{
		Resolver:        resolver,
		PlatformMatcher: pvd.platformMC,
	}

	img, err := fetch(ctx, pvd.ContentStore(), rc, ref, 0)
	if err != nil {
		return errors.Wrap(err, "pull source image")
	}
	pvd.setImage(ref, &img.Target)

	return nil
}

func (pvd *LocalProvider) Push(ctx context.Context, desc ocispec.Descriptor, ref string) error {
	resolver, err := pvd.Resolver(ref)
	if err != nil {
		return err
	}

	rc := &containerd.RemoteContext{
		Resolver:        resolver,
		PlatformMatcher: pvd.platformMC,
	}

	return push(ctx, pvd.ContentStore(), rc, desc, ref)
}

func (pvd *LocalProvider) Image(ctx context.Context, ref string) (*ocispec.Descriptor, error) {
	return pvd.getImage(ref)
}

func (pvd *LocalProvider) ContentStore() content.Store {
	return pvd.content.ContentStore()
}

func (pvd *LocalProvider) setImage(ref string, image *ocispec.Descriptor) {
	pvd.mutex.Lock()
	defer pvd.mutex.Unlock()
	pvd.images[ref] = image
}

func (pvd *LocalProvider) getImage(ref string) (*ocispec.Descriptor, error) {
	pvd.mutex.Lock()
	defer pvd.mutex.Unlock()
	if desc, ok := pvd.images[ref]; ok {
		return desc, nil
	}
	return nil, errdefs.ErrNotFound
}

func (pvd *LocalProvider) FetchRemoteCache(ctx context.Context, ref string) (*Cache, error) {

	resolver, err := pvd.Resolver(ref)
	if err != nil {
		return pvd.remoteCache, err
	}

	// TODO: sets max concurrent downloaded layer limit by containerd.WithMaxConcurrentDownloads.
	rc := &containerd.RemoteContext{
		Resolver:        resolver,
		PlatformMatcher: pvd.platformMC,
	}

	name, desc, err := rc.Resolver.Resolve(ctx, ref)
	if err != nil {
		return pvd.remoteCache, err
	}

	fetcher, err := rc.Resolver.Fetcher(ctx, name)
	if err != nil {
		return pvd.remoteCache, err
	}

	//fetch cache manifest
	ir, err := fetcher.Fetch(ctx, desc)
	if err != nil {
		if nydusErrdefs.NeedsRetryWithHTTP(err) {
			pvd.UsePlainHTTP()
			ir, err = fetcher.Fetch(ctx, desc)
			if err != nil {
				return nil, errors.Wrap(err, "try to pull remote cache")
			}
		} else {
			return pvd.remoteCache, errors.Wrap(err, "pull remote cache")
		}
	}

	bytes, err := io.ReadAll(ir)
	if err != nil {
		return pvd.remoteCache, err
	}

	manifest := ocispec.Manifest{}
	if err = json.Unmarshal(bytes, &manifest); err != nil {
		return pvd.remoteCache, err
	}

	pvd.remoteCache.Clear()
	for _, layer := range manifest.Layers {
		if layer.Annotations["containerd.io/snapshot/nydus-blob"] == "true" {
			pvd.remoteCache.Add(layer.Annotations["source image layer"], layer)
		}
	}
	return pvd.remoteCache, nil
}

// update remote cache and push to remote
func (pvd *LocalProvider) UpdateRemoteCache(ctx context.Context, remoteCache *Cache, ref string) error {

	if remoteCache == nil || remoteCache.Len() == 0 {
		return nil
	}

	// create an empty OCI Image Config
	imageConfig := ocispec.ImageConfig{}

	//make ocispec.Descriptor of imageConfig
	imageConfigBytes, err := json.Marshal(imageConfig)
	if err != nil {
		return err
	}
	imageConfigDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageConfig,
		Digest:    digest.FromBytes(imageConfigBytes),
		Size:      int64(len(imageConfigBytes)),
	}

	//make imageConfigBytes to io.Reader
	configReader := bytes.NewReader(imageConfigBytes)

	err = content.WriteBlob(ctx, pvd.ContentStore(), ref, configReader, imageConfigDesc)
	if err != nil {
		return errors.Wrap(err, "remote cache image config write blob failed")
	}

	layers := []ocispec.Descriptor{}
	for elem := remoteCache.ll.Front(); elem != nil; elem = elem.Next() {
		desc := elem.Value.(*entry).value.(ocispec.Descriptor)
		desc.Annotations["source image layer"] = elem.Value.(*entry).key.(string)
		layers = append(layers, desc)
	}

	manifest := ocispec.Manifest{
		Versioned: specs.Versioned{
			SchemaVersion: 2,
		},
		MediaType: ocispec.MediaTypeImageManifest,
		Config:    imageConfigDesc,
		Layers:    layers,
	}

	//make ocispec.Descriptor of manifest
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return err
	}
	manifestDesc := ocispec.Descriptor{
		MediaType: ocispec.MediaTypeImageManifest,
		Digest:    digest.FromBytes(manifestBytes),
		Size:      int64(len(manifestBytes)),
	}

	//make manifestBytes to io.Reader
	manifestReader := bytes.NewReader(manifestBytes)

	err = content.WriteBlob(ctx, pvd.ContentStore(), ref, manifestReader, manifestDesc)
	if err != nil {
		return errors.Wrap(err, "remote cache write blob failed")
	}
	err = pvd.Push(ctx, manifestDesc, ref)
	if err != nil {
		return err
	}

	return nil
}
