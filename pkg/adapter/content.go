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

package adapter

import (
	"context"
	"encoding/binary"
	"fmt"

	"github.com/containerd/containerd/metadata"
	"github.com/dustin/go-humanize"
	"github.com/goharbor/acceleration-service/pkg/config"
	"github.com/sirupsen/logrus"
	bolt "go.etcd.io/bbolt"
)

var (
	bucketKeyVersion       = []byte("v1")
	bucketKeyObjectContent = []byte("content")
	bucketKeyObjectBlob    = []byte("blob")

	bucketKeySize = []byte("size")
)

type Content struct {
	db        *metadata.DB
	threshold int64
}

func NewContent(db *metadata.DB, cfg *config.Config) (*Content, error) {
	threshold, err := humanize.ParseBytes(cfg.Provider.GCPolicy.Threshold)
	if err != nil {
		return nil, err
	}
	return &Content{
		db:        db,
		threshold: int64(threshold),
	}, nil
}

// Size return the size of local caches size
func (content *Content) Size() (int64, error) {
	var contentSize int64
	if err := content.db.View(func(tx *bolt.Tx) error {
		bucket := getBucket(tx, bucketKeyVersion, []byte("acceleration-service"), bucketKeyObjectContent, bucketKeyObjectBlob)
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

func blobSize(bkt *bolt.Bucket) (int64, error) {
	size, bytesRead := binary.Varint(bkt.Get(bucketKeySize))
	if bytesRead <= 0 {
		return 0, fmt.Errorf("read size from database")
	}
	return size, nil
}

func getBucket(tx *bolt.Tx, keys ...[]byte) *bolt.Bucket {
	bkt := tx.Bucket(keys[0])

	for _, key := range keys[1:] {
		if bkt == nil {
			break
		}
		bkt = bkt.Bucket(key)
	}

	return bkt
}
