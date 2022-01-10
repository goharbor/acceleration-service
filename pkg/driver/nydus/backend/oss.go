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

package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

const (
	splitPartsCount = 4
	// Blob size bigger than 100MB, apply multiparts upload.
	multipartsUploadThreshold = 100 * 1024 * 1024
)

type OSSBackend struct {
	// OSS storage does not support directory. Therefore add a prefix to each object
	// to make it a path-like object.
	objectPrefix string
	bucket       *oss.Bucket
}

func newOSSBackend(rawConfig []byte) (*OSSBackend, error) {
	var configMap map[string]string
	if err := json.Unmarshal(rawConfig, &configMap); err != nil {
		return nil, errors.Wrap(err, "Parse OSS storage backend configuration")
	}

	endpoint, ok1 := configMap["endpoint"]
	bucketName, ok2 := configMap["bucket_name"]

	// Below fields are not mandatory.
	accessKeyID := configMap["access_key_id"]
	accessKeySecret := configMap["access_key_secret"]
	objectPrefix := configMap["object_prefix"]

	if !ok1 || !ok2 {
		return nil, fmt.Errorf("no endpoint or bucket is specified")
	}

	client, err := oss.New(endpoint, accessKeyID, accessKeySecret)
	if err != nil {
		return nil, errors.Wrap(err, "Create client")
	}

	bucket, err := client.Bucket(bucketName)
	if err != nil {
		return nil, errors.Wrap(err, "Create bucket")
	}

	return &OSSBackend{
		objectPrefix: objectPrefix,
		bucket:       bucket,
	}, nil
}

// Upload nydus blob to oss storage backend, depending on blob's size,
// upload it by multiparts method or the normal method.
func (b *OSSBackend) Push(ctx context.Context, blobPath string) error {
	blobID := filepath.Base(blobPath)
	blobObjectKey := b.objectPrefix + blobID

	if exist, err := b.bucket.IsObjectExist(blobObjectKey); err != nil {
		return errors.Wrap(err, "check object existence")
	} else if exist {
		logrus.Infof("skip upload because blob exists: %s", blobID)
		return nil
	}

	var stat os.FileInfo
	stat, err := os.Stat(blobPath)
	if err != nil {
		return errors.Wrap(err, "stat blob file")
	}
	blobSize := stat.Size()

	needMultiparts := false
	// Blob size bigger than multipartsUploadThreshold, apply multiparts upload.
	if blobSize >= multipartsUploadThreshold {
		needMultiparts = true
	}

	if needMultiparts {
		logrus.Debugf("upload %s using multiparts method", blobObjectKey)
		chunks, err := oss.SplitFileByPartNum(blobPath, splitPartsCount)
		if err != nil {
			return errors.Wrap(err, "split blob file")
		}

		imur, err := b.bucket.InitiateMultipartUpload(blobObjectKey)
		if err != nil {
			return errors.Wrap(err, "initiate blob multipart upload")
		}

		// FIXME: it always splits the blob into splitPartsCount parts.
		partsChan := make(chan oss.UploadPart, splitPartsCount)

		g := new(errgroup.Group)
		for _, chunk := range chunks {
			ck := chunk
			g.Go(func() error {
				p, err := b.bucket.UploadPartFromFile(imur, blobPath, ck.Offset, ck.Size, ck.Number)
				if err != nil {
					return errors.Wrap(err, "upload part from file")
				}
				partsChan <- p
				return nil
			})
		}

		if err := g.Wait(); err != nil {
			b.bucket.AbortMultipartUpload(imur)
			close(partsChan)
			return err
		}

		close(partsChan)

		var parts []oss.UploadPart
		for p := range partsChan {
			parts = append(parts, p)
		}

		_, err = b.bucket.CompleteMultipartUpload(imur, parts)
		if err != nil {
			return errors.Wrap(err, "complete multipart upload")
		}
	} else {
		reader, err := os.Open(blobPath)
		if err != nil {
			return errors.Wrap(err, "open blob file")
		}
		defer reader.Close()
		err = b.bucket.PutObject(blobObjectKey, reader)
		if err != nil {
			return errors.Wrap(err, "put object")
		}
	}

	return nil
}

func (b *OSSBackend) Check(blobID string) (bool, error) {
	blobID = b.objectPrefix + blobID
	return b.bucket.IsObjectExist(blobID)
}

func (b *OSSBackend) Type() string {
	return BackendTypeOSS
}
