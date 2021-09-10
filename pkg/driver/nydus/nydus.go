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

package nydus

import (
	"context"
	"io/ioutil"
	"os"
	"path"
	"sync"

	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"

	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/platforms"
	digest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/identity"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/goharbor/acceleration-service/pkg/content"
	"github.com/goharbor/acceleration-service/pkg/driver/nydus/builder"
	"github.com/goharbor/acceleration-service/pkg/driver/nydus/export"
	"github.com/goharbor/acceleration-service/pkg/driver/nydus/mount"
)

type Driver struct {
	workDir string
	builder *builder.Builder
}

func New(cfg map[string]string) (*Driver, error) {
	workDir := cfg["work_dir"]
	if workDir == "" {
		workDir = os.TempDir()
	}

	builderPath := cfg["builder_path"]
	if builderPath == "" {
		builderPath = "nydus-image"
	}

	return &Driver{
		workDir: workDir,
		builder: builder.New(builderPath),
	}, nil
}

func (nydus *Driver) Convert(ctx context.Context, content content.Provider) (*ocispec.Descriptor, error) {
	diffIDs, err := content.Image().RootFS(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "get diff ids from containerd")
	}

	sourceManifest, err := images.Manifest(ctx, content.Image().ContentStore(), content.Image().Target(), platforms.Default())
	if err != nil {
		return nil, errors.Wrap(err, "get image manifest from containerd")
	}

	var chain []digest.Digest
	var wg sync.WaitGroup

	done := make(chan bool)
	paths := make([]string, len(sourceManifest.Layers))
	hintPaths := make([]string, len(sourceManifest.Layers))
	snapshotter := content.Snapshotter()
	eg, ctx := errgroup.WithContext(ctx)

	for idx := range sourceManifest.Layers {
		lower := identity.ChainID(chain).String()
		chain = append(chain, diffIDs[idx])
		upper := identity.ChainID(chain).String()

		wg.Add(1)
		eg.Go(func(idx int) func() error {
			return func() error {
				if err := mount.Mount(ctx, snapshotter, lower, upper, func(lowerRoot, upperRoot, upperSnapshot string) error {
					paths[idx] = upperRoot
					hintPaths[idx] = upperSnapshot
					wg.Done()
					<-done
					return nil
				}); err != nil {
					wg.Done()
					return errors.Wrap(err, "mount layer of source image")
				}
				return nil
			}
		}(idx))
	}
	wg.Wait()

	workDir, err := ioutil.TempDir(nydus.workDir, "harbor-acceleration-service-nydus-driver-workdir")
	if err != nil {
		return nil, errors.Wrapf(err, "create temp dir")
	}
	defer os.RemoveAll(workDir)

	bootstrapPath := path.Join(workDir, "bootstrap")
	outputJSONPath := path.Join(workDir, "output.json")

	blobs, err := nydus.builder.Run(builder.Option{
		BootstrapPath: bootstrapPath,
		BlobDirPath:   workDir,

		DiffLayerPaths: paths,
		HintLayerPaths: hintPaths,

		OutputJSONPath: outputJSONPath,
	})
	if err != nil {
		return nil, errors.Wrapf(err, "build layers %v %v", paths, hintPaths)
	}

	layers := []ocispec.Descriptor{}
	for _, blobID := range blobs {
		blobPath := path.Join(workDir, blobID)
		blobDesc, err := export.WriteBlob(ctx, content, blobPath)
		if err != nil {
			return nil, errors.Wrap(err, "write blob")
		}
		layers = append(layers, *blobDesc)
	}
	bootstrapDesc, err := export.WriteBootstrap(ctx, content, bootstrapPath, blobs)
	if err != nil {
		return nil, errors.Wrap(err, "write bootstrap")
	}
	layers = append(layers, *bootstrapDesc)

	nydusManifestDesc, err := export.Export(ctx, content, layers)
	if err != nil {
		return nil, errors.Wrap(err, "export manifest")
	}

	close(done)

	if err := eg.Wait(); err != nil {
		return nil, errors.Wrap(err, "wait all layer mount")
	}

	return nydusManifestDesc, nil
}
