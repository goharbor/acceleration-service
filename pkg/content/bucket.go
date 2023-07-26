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
	"encoding/binary"
	"fmt"

	"github.com/opencontainers/go-digest"
	bolt "go.etcd.io/bbolt"
)

var (
	bucketKeyObjectContent = []byte("content")
	bucketKeyObjectBlob    = []byte("blob")

	bucketKeyVersion   = []byte("v1")
	bucketKeyNamespace = []byte(accelerationServiceNamespace)
	bucketKeySize      = []byte("size")
	bucketKeyUpdatedAt = []byte("updatedat")
)

const accelerationServiceNamespace = "acceleration-service"

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

// getBlobsBucket return all blob buckets in a bucket
func getBlobsBucket(tx *bolt.Tx) *bolt.Bucket {
	return getBucket(tx, bucketKeyVersion, bucketKeyNamespace, bucketKeyObjectContent, bucketKeyObjectBlob)
}

// getBlobBucket return the blob bucket by digest
func getBlobBucket(tx *bolt.Tx, digst digest.Digest) *bolt.Bucket {
	return getBucket(tx, bucketKeyVersion, bucketKeyNamespace, bucketKeyObjectContent, bucketKeyObjectBlob, []byte(digst.String()))
}

// bolbSize return the content blob size in a bucket
func blobSize(bucket *bolt.Bucket) (int64, error) {
	size, bytesRead := binary.Varint(bucket.Get(bucketKeySize))
	if bytesRead <= 0 {
		return 0, fmt.Errorf("read size from database")
	}
	return size, nil
}
