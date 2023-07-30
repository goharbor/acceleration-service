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
	"path/filepath"
	"time"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/content/local"
	"github.com/containerd/containerd/metadata"
	"github.com/dustin/go-humanize"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	bolt "go.etcd.io/bbolt"
)

type Content struct {
	// db is the bolt database of content
	db *metadata.DB
	// store is the local content store wrapped inner db
	store content.Store
	// threshold is the maximum capacity of the local caches storage
	threshold int64
}

// NewContent return content support by content store, bolt database and threshold.
// content store created in contentDir and  bolt database created in databaseDir.
// content.db supported by bolt database and content store.
func NewContent(contentDir string, databaseDir string, threshold string) (*Content, error) {
	store, err := local.NewLabeledStore(contentDir, newMemoryLabelStore())
	if err != nil {
		return nil, errors.Wrap(err, "create local provider content store")
	}
	bdb, err := bolt.Open(filepath.Join(databaseDir, "meta.db"), 0655, nil)
	if err != nil {
		return nil, errors.Wrap(err, "create local provider database")
	}
	db := metadata.NewDB(bdb, store, nil)
	if err := db.Init(context.Background()); err != nil {
		return nil, err
	}
	t, err := humanize.ParseBytes(threshold)
	if err != nil {
		return nil, err
	}
	content := Content{
		db:        db,
		store:     db.ContentStore(),
		threshold: int64(t),
	}
	return &content, nil
}

// Size return the size of local caches size
func (content *Content) Size() (int64, error) {
	var contentSize int64
	if err := content.db.View(func(tx *bolt.Tx) error {
		bucket := getBlobsBucket(tx)
		// if can't find blob bucket, it maens content store is empty
		if bucket == nil {
			return nil
		}
		return bucket.ForEach(func(key, value []byte) error {
			if subBucket := bucket.Bucket(key); subBucket != nil {
				size, err := blobSize(subBucket)
				if err != nil {
					return err
				}
				contentSize += size
			}
			return nil
		})
	}); err != nil {
		return 0, err
	}
	return contentSize, nil
}

// GC clean the local caches by cfg.Provider.GCPolicy configuration
func (content *Content) GC(ctx context.Context) error {
	size, err := content.Size()
	if err != nil {
		return err
	}
	if size > content.threshold {
		// TODO: *metadata.DB.GarbageCollect will clear all caches, we need to rewrite gc
		gcStatus, err := content.db.GarbageCollect(ctx)
		if err != nil {
			return err
		}
		logrus.Infof("garbage collect, elapse %s", gcStatus.Elapsed())
	}
	return nil
}

// updateTime update the latest used time
func (content *Content) updateTime(digest *digest.Digest) error {
	return content.db.Update(func(tx *bolt.Tx) error {
		bucket := getBlobBucket(tx, *digest)
		updatedAt, err := time.Now().UTC().MarshalBinary()
		if err != nil {
			return err
		}
		return bucket.Put(bucketKeyUpdatedAt, updatedAt)
	})
}

func (content *Content) Info(ctx context.Context, dgst digest.Digest) (content.Info, error) {
	return content.store.Info(ctx, dgst)
}

func (content *Content) Update(ctx context.Context, info content.Info, fieldpaths ...string) (content.Info, error) {
	return content.store.Update(ctx, info, fieldpaths...)
}

func (content *Content) Walk(ctx context.Context, fn content.WalkFunc, filters ...string) error {
	return content.store.Walk(ctx, fn, filters...)
}

func (content *Content) Delete(ctx context.Context, dgst digest.Digest) error {
	return content.store.Delete(ctx, dgst)
}

func (content *Content) ReaderAt(ctx context.Context, desc ocispec.Descriptor) (content.ReaderAt, error) {
	readerAt, err := content.store.ReaderAt(ctx, desc)
	if err != nil {
		return readerAt, err
	}
	return readerAt, content.updateTime(&desc.Digest)
}

func (content *Content) Status(ctx context.Context, ref string) (content.Status, error) {
	return content.store.Status(ctx, ref)
}

func (content *Content) ListStatuses(ctx context.Context, filters ...string) ([]content.Status, error) {
	return content.store.ListStatuses(ctx, filters...)
}

func (content *Content) Abort(ctx context.Context, ref string) error {
	return content.store.Abort(ctx, ref)
}

func (content *Content) Writer(ctx context.Context, opts ...content.WriterOpt) (content.Writer, error) {
	return content.store.Writer(ctx, opts...)
}
