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

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

const (
	BackendTypeLocalFS = "localfs"
)

type LocalFSBackend struct {
	dir string
}

func moveFile(src, dst string) error {
	err := os.Rename(src, dst)
	if err != nil {
		logrus.WithError(err).Warnf("failed to rename %s to %s, try to copy", src, dst)
	} else {
		return nil
	}

	srcFile, err := os.Open(src)
	if err != nil {
		return errors.Wrapf(err, "open source file: %s", src)
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return errors.Wrapf(err, "create destination file: %s", dst)
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	if err != nil {
		return errors.Wrapf(err, "copy file %s to %s", src, dst)
	}

	return nil
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

func (b *LocalFSBackend) Push(ctx context.Context, blobPath string) error {
	if err := os.MkdirAll(b.dir, 0755); err != nil {
		return errors.Wrap(err, "create directory in localfs backend")
	}

	blobID := path.Base(blobPath)
	dstPath := b.dstPath(blobID)

	if err := moveFile(blobPath, dstPath); err != nil {
		return errors.Wrap(err, "move blob file in localfs backend")
	}

	return nil
}

func (b *LocalFSBackend) Check(blobID string) (bool, error) {
	dstPath := b.dstPath(blobID)

	info, err := os.Stat(dstPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}

	return !info.IsDir(), nil
}

func (b *LocalFSBackend) Type() string {
	return BackendTypeLocalFS
}
