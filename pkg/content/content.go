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
	"encoding/binary"
	"fmt"
	"sort"
	"time"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/leases"
	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/metadata/boltutil"
	"github.com/containerd/containerd/namespaces"
	"github.com/dustin/go-humanize"
	"github.com/opencontainers/go-digest"
	"github.com/sirupsen/logrus"
	bolt "go.etcd.io/bbolt"
)

var (
	bucketKeyVersion       = []byte("v1")
	bucketKeyObjectContent = []byte("content")
	bucketKeyObjectBlob    = []byte("blob")
	bucketKeyObjectLeases  = []byte("leases")

	bucketKeySize  = []byte("size")
	updatedAtLabel = "updatedat"
)

type Content struct {
	db        *metadata.DB
	lm        leases.Manager
	threshold int64
}

func NewContent(db *metadata.DB, threshold string) (*Content, error) {
	t, err := humanize.ParseBytes(threshold)
	if err != nil {
		return nil, err
	}
	return &Content{
		db:        db,
		lm:        metadata.NewLeaseManager(db),
		threshold: int64(t),
	}, nil
}

// return the content store in db
func (content *Content) ContentStore() content.Store {
	return content.db.ContentStore()
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

func blobSize(bucket *bolt.Bucket) (int64, error) {
	size, bytesRead := binary.Varint(bucket.Get(bucketKeySize))
	if bytesRead <= 0 {
		return 0, fmt.Errorf("read size from database")
	}
	return size, nil
}

// GC clean the local caches by cfg.Provider.GCPolicy configuration
func (content *Content) GC(ctx context.Context) error {
	//clear the intermediate file created in convert
	if _, err := content.db.GarbageCollect(ctx); err != nil {
		return err
	}
	size, err := content.Size()
	if err != nil {
		return err
	}
	if size > content.threshold {
		if err := content.manageLeases(ctx, size-content.threshold); err != nil {
			return err
		}
		gcStatus, err := content.db.GarbageCollect(ctx)
		if err != nil {
			return err
		}
		logrus.Infof("garbage collect, elapse %s", gcStatus.Elapsed())
	}
	return nil
}

// use lease to manage content blob, delete lease of content which should be gc
func (content *Content) manageLeases(ctx context.Context, size int64) error {
	leases, err := content.lm.List(ctx)
	if err != nil {
		return nil
	}
	sort.Slice(leases, func(i, j int) bool {
		return leases[i].Labels[updatedAtLabel] < leases[j].Labels[updatedAtLabel]
	})
	for _, lease := range leases {
		content.db.View(func(tx *bolt.Tx) error {
			blobsize, err := blobSize(getBlobsBucket(tx).Bucket([]byte(lease.ID)))
			if err != nil {
				return err
			}
			size -= blobsize
			content.lm.Delete(ctx, lease)
			return nil
		})
		if size <= 0 {
			break
		}
	}
	return nil
}

// update the latest used time in lease
func (content *Content) UpdateTime(digest *digest.Digest) error {
	var l leases.Lease
	ctx := namespaces.WithNamespace(context.Background(), "acceleration-service")
	contentLeases, err := content.lm.List(ctx, "id=="+digest.String())
	if err != nil {
		return nil
	}
	if len(contentLeases) == 0 {
		l, err = content.lm.Create(ctx, leases.WithID(digest.String()))
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
		bucket := getLeasesBucket(tx, digest.String())
		// if can't find lease bucket, it maens content store is empty
		if bucket == nil {
			return nil
		}
		updatedLabel := map[string]string{}
		updatedLabel[updatedAtLabel] = time.Now().UTC().String()
		return boltutil.WriteLabels(bucket, updatedLabel)
	})
}

func getBucket(tx *bolt.Tx, keys ...[]byte) *bolt.Bucket {
	bucket := tx.Bucket(keys[0])

	for _, key := range keys[1:] {
		if bucket == nil {
			break
		}
		bucket = bucket.Bucket(key)
	}

	return bucket
}

func getBlobsBucket(tx *bolt.Tx) *bolt.Bucket {
	return getBucket(tx, bucketKeyVersion, []byte("acceleration-service"), bucketKeyObjectContent, bucketKeyObjectBlob)
}

func getLeasesBucket(tx *bolt.Tx, lease string) *bolt.Bucket {
	return getBucket(tx, bucketKeyVersion, []byte("acceleration-service"), bucketKeyObjectLeases, []byte(lease))
}
