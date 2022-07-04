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
	"io"

	"github.com/aliyun/aliyun-oss-go-sdk/oss"
	"github.com/containerd/containerd/errdefs"
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
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

// Upload nydus blob to oss storage backend.
func (b *OSSBackend) Push(ctx context.Context, blobReader io.Reader, blobDigest digest.Digest) error {
	blobID := blobDigest.Hex()
	blobObjectKey := b.objectPrefix + blobID

	if exist, err := b.bucket.IsObjectExist(blobObjectKey); err != nil {
		return errors.Wrap(err, "check object existence")
	} else if exist {
		logrus.Infof("skip upload because blob exists: %s", blobID)
		return nil
	}

	// FIXME: handle large blob over 5GB.
	if err := b.bucket.PutObject(blobObjectKey, blobReader); err != nil {
		return errors.Wrap(err, "put blob object")
	}

	return nil
}

func (b *OSSBackend) Check(blobDigest digest.Digest) (string, error) {
	blobID := blobDigest.Hex()
	blobObjectKey := b.objectPrefix + blobID
	if exist, err := b.bucket.IsObjectExist(blobObjectKey); err != nil {
		return "", err
	} else if exist {
		return blobID, nil
	}
	return "", errdefs.ErrNotFound
}

func (b *OSSBackend) Type() string {
	return BackendTypeOSS
}
