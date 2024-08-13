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
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	ctrcontent "github.com/containerd/containerd/content"
	"github.com/containerd/containerd/content/local"
	"github.com/containerd/containerd/leases"
	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/metadata/boltutil"
	"github.com/containerd/containerd/namespaces"
	"github.com/containerd/errdefs"
	"github.com/dustin/go-humanize"

	"github.com/goharbor/acceleration-service/pkg/cache"
	"github.com/goharbor/acceleration-service/pkg/remote"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	bolt "go.etcd.io/bbolt"
	"golang.org/x/sync/singleflight"
)

const gcPercent int64 = 80

type Content struct {
	// db is the bolt database of content
	db *metadata.DB
	// lm is lease manager for managing leases using the provided database transaction.
	lm leases.Manager
	// gcSingleflight help to resolve concurrent gc
	gcSingleflight *singleflight.Group
	// GcMutex works between gc and convert
	GcMutex *sync.RWMutex
	// lc cache the used count and reference order of lease
	lc *leaseCache
	// store is the local content store wrapped inner db
	store ctrcontent.Store
	// hosts provides remote registry access methods.
	hosts remote.HostFunc
	// Threshold is the maximum capacity of the local caches storage
	Threshold int64
}

// NewContent return content support by content store, bolt database and threshold.
// content store created in contentDir and  bolt database created in databaseDir.
// content.db supported by bolt database and content store, content.lm supported by content.db.
func NewContent(hosts remote.HostFunc, contentDir string, databaseDir string, threshold string) (*Content, error) {
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
	lm := metadata.NewLeaseManager(db)
	lc := newLeaseCache()
	if err := lc.Init(lm); err != nil {
		return nil, err
	}
	content := Content{
		db:             db,
		lm:             metadata.NewLeaseManager(db),
		gcSingleflight: &singleflight.Group{},
		GcMutex:        &sync.RWMutex{},
		lc:             lc,
		store:          db.ContentStore(),
		hosts:          hosts,
		Threshold:      int64(t),
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
		return bucket.ForEach(func(key, _ []byte) error {
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
func (content *Content) GC(ctx context.Context, threshold int64) {
	size, err := content.Size()
	if err != nil {
		logrus.Error(errors.Wrap(err, "gc get content size"))
		return
	}
	// if the local content size over eighty percent of threshold, gc start
	if size > (threshold*gcPercent)/100 {
		content.gcSingleflight.Do(accelerationServiceNamespace, func() (interface{}, error) {
			content.GcMutex.Lock()
			defer content.GcMutex.Unlock()
			// recalculate the local cache size
			size, err := content.Size()
			if err != nil {
				logrus.Error(errors.Wrap(err, "gc get content size"))
				return nil, nil
			}
			if err := content.garbageCollect(ctx, size-(threshold*gcPercent)/100); err != nil {
				logrus.Error(errors.Wrap(err, "gc"))
			}
			return nil, nil
		})
	}
}

// garbageCollect clean the local caches by lease
func (content *Content) garbageCollect(ctx context.Context, size int64) error {
	if err := content.cleanLeases(ctx, size); err != nil {
		return err
	}
	gcStatus, err := content.db.GarbageCollect(ctx)
	if err != nil {
		return err
	}
	logrus.Infof("garbage collect, elapse %s", gcStatus.Elapsed())
	return nil
}

// cleanLeases use lease to manage content blob, delete lease of content which should be gc
func (content *Content) cleanLeases(ctx context.Context, size int64) error {
	for size > 0 {
		if content.lc.Len() == 0 {
			return fmt.Errorf("cleanLeases: leaseCache is empty, error caches")
		}
		digest, err := content.lc.Remove()
		if err != nil {
			return err
		}
		if err := content.db.View(func(tx *bolt.Tx) error {
			blobsize, err := blobSize(getBlobsBucket(tx).Bucket([]byte(digest)))
			if err != nil {
				return err
			}
			size -= blobsize
			return nil
		}); err != nil {
			return err
		}
		contentLease, err := content.lm.List(ctx, "id=="+digest)
		if err != nil {
			return err
		}
		if len(contentLease) != 1 {
			return fmt.Errorf("cleanLeases: find lease by digest failed")
		}
		if err := content.lm.Delete(ctx, contentLease[0]); err != nil {
			return err
		}
	}
	return nil
}

// updateLease update the latest used time and used counts in lease
func (content *Content) updateLease(digest *digest.Digest) error {
	ctx := namespaces.WithNamespace(context.Background(), accelerationServiceNamespace)
	contentLeases, err := content.lm.List(ctx, "id=="+digest.String())
	if err != nil {
		return err
	}
	if len(contentLeases) == 0 {
		l, err := content.lm.Create(ctx, leases.WithID(digest.String()))
		if err != nil {
			return err
		}
		if err := content.lm.AddResource(ctx, l, leases.Resource{
			ID:   digest.String(),
			Type: "content",
		}); err != nil {
			return err
		}
	}
	return content.db.Update(func(tx *bolt.Tx) error {
		bucket := getLeaseBucket(tx, digest.String())
		// if can't find lease bucket, it maens content store is empty
		if bucket == nil {
			return nil
		}
		// read the labels from lease bucket
		labels, err := boltutil.ReadLabels(bucket)
		if err != nil {
			return err
		}
		// update the usedCountLabel
		usedCount := 1
		count, ok := labels[usedCountLabel]
		if ok {
			usedCount, err = strconv.Atoi(count)
			if err != nil {
				return err
			}
			usedCount++
		}
		if err := content.lc.Add(digest.String(), strconv.Itoa(usedCount)); err != nil {
			return err
		}
		// write the new labels into lease bucket
		return boltutil.WriteLabels(bucket, map[string]string{
			usedCountLabel: strconv.Itoa(usedCount),
			usedAtLabel:    time.Now().UTC().String(),
		})
	})
}

func (content *Content) Info(ctx context.Context, dgst digest.Digest) (ctrcontent.Info, error) {
	if _, cached := cache.Get(ctx, dgst); cached != nil {
		return ctrcontent.Info{
			Digest: cached.Digest,
			Size:   cached.Size,
			Labels: cached.Annotations,
		}, nil
	}

	return content.store.Info(ctx, dgst)
}

func (content *Content) Update(ctx context.Context, info ctrcontent.Info, fieldpaths ...string) (ctrcontent.Info, error) {
	// containerd content store write labels to annotate some blobs belong to a same repo,
	// cleaning gc related labels
	for k := range info.Labels {
		if strings.HasPrefix(k, "containerd.io/gc") {
			delete(info.Labels, k)
		}
	}

	updatedInfo, err := content.store.Update(ctx, info, fieldpaths...)
	if errors.Is(err, errdefs.ErrNotFound) {
		if _, cached := cache.Update(ctx, info.Digest, info.Labels); cached != nil {
			return ctrcontent.Info{
				Digest: cached.Digest,
				Size:   cached.Size,
				Labels: cached.Annotations,
			}, nil
		}
	}
	return updatedInfo, err
}

func (content *Content) Walk(ctx context.Context, fn ctrcontent.WalkFunc, fs ...string) error {
	return content.store.Walk(ctx, fn, fs...)
}

func (content *Content) Delete(ctx context.Context, dgst digest.Digest) error {
	return content.store.Delete(ctx, dgst)
}

func (content *Content) ReaderAt(ctx context.Context, desc ocispec.Descriptor) (ctrcontent.ReaderAt, error) {
	defer content.updateLease(&desc.Digest)

	ra, err := content.store.ReaderAt(ctx, desc)
	if errors.Is(err, errdefs.ErrNotFound) {
		if rc, cached := cache.Get(ctx, desc.Digest); cached != nil {
			return remote.Fetch(ctx, rc.Ref, desc, content.hosts, true)
		}
	}

	return ra, err
}

func (content *Content) Status(ctx context.Context, ref string) (ctrcontent.Status, error) {
	return content.store.Status(ctx, ref)
}

func (content *Content) ListStatuses(ctx context.Context, filters ...string) ([]ctrcontent.Status, error) {
	return content.store.ListStatuses(ctx, filters...)
}

func (content *Content) Abort(ctx context.Context, ref string) error {
	return content.store.Abort(ctx, ref)
}

func (content *Content) Writer(ctx context.Context, opts ...ctrcontent.WriterOpt) (ctrcontent.Writer, error) {
	wopts := ctrcontent.WriterOpts{}
	for _, opt := range opts {
		opt(&wopts)
	}
	if wopts.Desc.Digest != "" {
		if _, cached := cache.Update(ctx, wopts.Desc.Digest, wopts.Desc.Annotations); cached != nil {
			return nil, errdefs.ErrAlreadyExists
		}
	}

	writer, err := content.store.Writer(ctx, opts...)
	return &localWriter{writer, content}, err
}

// localWriter wrap the content.Writer
type localWriter struct {
	ctrcontent.Writer
	content *Content
}

func (localWriter localWriter) Commit(ctx context.Context, size int64, expected digest.Digest, _ ...ctrcontent.Opt) error {
	// we don't write any lables, drop the opts
	localWriter.content.updateLease(&expected)
	return localWriter.Writer.Commit(ctx, size, expected)
}
