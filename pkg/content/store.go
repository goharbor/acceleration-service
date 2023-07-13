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

	containerdContent "github.com/containerd/containerd/content"
	"github.com/containerd/containerd/content/local"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// Store wrap the content store to complete custom feature
type Store struct {
	// store is content store
	store containerdContent.Store
	// content is related to database
	content *Content
}

// newStore returns a content store
func newStore(contentDir string) (*Store, error) {
	store, err := local.NewLabeledStore(contentDir, newMemoryLabelStore())
	return &Store{
		store: store,
	}, err
}

func (store *Store) Init(content *Content) {
	store.content = content
}

func (store *Store) Info(ctx context.Context, dgst digest.Digest) (containerdContent.Info, error) {
	return store.store.Info(ctx, dgst)
}

func (store *Store) Update(ctx context.Context, info containerdContent.Info, fieldpaths ...string) (containerdContent.Info, error) {
	return store.store.Update(ctx, info, fieldpaths...)
}

func (store *Store) Walk(ctx context.Context, fn containerdContent.WalkFunc, filters ...string) error {
	return store.store.Walk(ctx, fn, filters...)
}

func (store *Store) Delete(ctx context.Context, dgst digest.Digest) error {
	return store.store.Delete(ctx, dgst)
}

func (store *Store) ReaderAt(ctx context.Context, desc ocispec.Descriptor) (containerdContent.ReaderAt, error) {
	readerAt, err := store.store.ReaderAt(ctx, desc)
	if err != nil {
		return readerAt, err
	}
	return readerAt, store.content.UpdateTime(&desc.Digest)
}

func (store *Store) Status(ctx context.Context, ref string) (containerdContent.Status, error) {
	return store.store.Status(ctx, ref)
}

func (store *Store) ListStatuses(ctx context.Context, filters ...string) ([]containerdContent.Status, error) {
	return store.store.ListStatuses(ctx, filters...)
}

func (store *Store) Abort(ctx context.Context, ref string) error {
	return store.store.Abort(ctx, ref)
}

func (store *Store) Writer(ctx context.Context, opts ...containerdContent.WriterOpt) (containerdContent.Writer, error) {
	return store.store.Writer(ctx, opts...)
}
