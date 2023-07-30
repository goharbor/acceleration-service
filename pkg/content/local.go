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
	"os"
	"path/filepath"
	"sync"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/errdefs"
	"github.com/containerd/containerd/platforms"
	"github.com/containerd/containerd/remotes"
	"github.com/goharbor/acceleration-service/pkg/remote"
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
		content:    content,
		images:     make(map[string]*ocispec.Descriptor),
		hosts:      hosts,
		platformMC: platformMC,
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
	return pvd.content
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
