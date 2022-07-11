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
	"os"
	"path"

	"github.com/containerd/containerd/errdefs"
	"github.com/opencontainers/go-digest"
	"github.com/pkg/errors"
)

const (
	BackendTypeLocalFS = "localfs"
)

type LocalFSBackend struct {
	dir string
}

func newLocalFSBackend(rawConfig []byte) (*LocalFSBackend, error) {
	var configMap map[string]string
	if err := json.Unmarshal(rawConfig, &configMap); err != nil {
		return nil, errors.Wrap(err, "parse LocalFS storage backend configuration")
	}

	dir, ok := configMap["dir"]
	if !ok {
		return nil, fmt.Errorf("no `dir` option is specified")
	}

	return &LocalFSBackend{
		dir: dir,
	}, nil
}

func (b *LocalFSBackend) dstPath(blobID string) string {
	return path.Join(b.dir, blobID)
}

func (b *LocalFSBackend) Push(ctx context.Context, blobReader io.Reader, blobDigest digest.Digest) error {
	if err := os.MkdirAll(b.dir, 0755); err != nil {
		return errors.Wrap(err, "create directory in localfs backend")
	}

	blobID := blobDigest.Hex()
	dstPath := b.dstPath(blobID)

	dstFile, err := os.Create(dstPath)
	if err != nil {
		return errors.Wrapf(err, "create destination file: %s", dstPath)
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, blobReader); err != nil {
		return errors.Wrapf(err, "copy blob to %s", dstPath)
	}

	return nil
}

func (b *LocalFSBackend) Check(blobDigest digest.Digest) (string, error) {
	dstPath := b.dstPath(blobDigest.Hex())

	info, err := os.Stat(dstPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", errdefs.ErrNotFound
		}
		return "", err
	}

	if !info.IsDir() {
		return dstPath, nil
	}

	return "", errdefs.ErrNotFound
}

func (b *LocalFSBackend) Type() string {
	return BackendTypeLocalFS
}
