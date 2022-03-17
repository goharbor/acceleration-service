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

package packer

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path"

	"github.com/containerd/containerd/content"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"

	"github.com/goharbor/acceleration-service/pkg/driver/nydus/builder"
	"github.com/goharbor/acceleration-service/pkg/driver/nydus/utils"
)

type Descriptor struct {
	Blobs     []ocispec.Descriptor
	Bootstrap ocispec.Descriptor
}

// Packer implements the build workflow of nydus image, as well as
// the export and import of build cache.
type Packer struct {
	parentWorkDir string
	builderPath   string
	rafsVersion   string
	flatten       bool
	sg            singleflight.Group
	cachedBlobs   map[digest.Digest]*ocispec.Descriptor
}

type Option struct {
	WorkDir     string
	BuilderPath string
	RafsVersion string
	Flatten     bool
	ChunkDict   *ChunkDict
}

type ChunkDict struct {
	BootstrapPath string
	Blobs         map[string]ocispec.Descriptor
}

func New(option Option) (*Packer, error) {
	return &Packer{
		parentWorkDir: option.WorkDir,
		builderPath:   option.BuilderPath,
		rafsVersion:   option.RafsVersion,
		cachedBlobs:   make(map[digest.Digest]*ocispec.Descriptor),
		flatten:       option.Flatten,
	}, nil
}

func getDeltaBlobs(pre, cur []ocispec.Descriptor) []ocispec.Descriptor {
	preMap := map[string]ocispec.Descriptor{}

	for _, desc := range pre {
		preMap[desc.Digest.String()] = desc
	}

	delta := []ocispec.Descriptor{}
	for _, desc := range cur {
		if _, ok := preMap[desc.Digest.String()]; !ok {
			delta = append(delta, desc)
		}
	}

	return delta
}

func (p *Packer) prepareWorkdir() (string, func() error, error) {
	workDir, err := ioutil.TempDir(p.parentWorkDir, "nydus-build-")
	if err != nil {
		return "", nil, errors.Wrapf(err, "create work dir")
	}

	// Create a directory to store nydus blob file for every layer.
	blobDirPath := path.Join(workDir, "blobs")
	if err := os.MkdirAll(blobDirPath, 0755); err != nil {
		return "", nil, errors.Wrapf(err, "create blob dir %s", blobDirPath)
	}

	// Create a directory to store nydus bootstrap file for every layer.
	bootstrapDirPath := path.Join(workDir, "bootstraps")
	if err := os.MkdirAll(bootstrapDirPath, 0755); err != nil {
		return "", nil, errors.Wrapf(err, "create bootstrap dir %s", bootstrapDirPath)
	}

	cleanup := func() error {
		return os.RemoveAll(workDir)
	}

	return workDir, cleanup, nil
}

func (p *Packer) diffBuild(ctx context.Context, workDir string, chunkDict *ChunkDict, layers []*BuildLayer, diffSkip *int) (*builder.Output, error) {
	diffPaths := []string{}
	diffHintPaths := []string{}

	bootstrapDir := path.Join(workDir, "bootstraps")
	blobDir := path.Join(workDir, "blobs")
	outputJSONPath := path.Join(workDir, "output.json")

	for _, layer := range layers {
		diffPaths = append(diffPaths, layer.diffPath)
		diffHintPaths = append(diffHintPaths, layer.diffHintPath)
	}

	var parentBootstrapPath *string
	if diffSkip != nil {
		// Found nydus bootstrap cache, unpack targz and use it as parent bootstrap.
		_parentBootstrapPath := path.Join(bootstrapDir, "parent-bootstrap")
		parentBootstrapPath = &_parentBootstrapPath
		layer := layers[*diffSkip]

		// If err != nil, the cache should be considered miss and the error should
		// be ignored in order not to affect the next workflow.
		cachedBootstrap, _, err := layer.GetCache(ctx)
		if err != nil {
			return nil, fmt.Errorf("can't find cache")
		}
		ra, err := layer.ContentStore(ctx).ReaderAt(ctx, ocispec.Descriptor{
			Digest: cachedBootstrap.Digest,
			Size:   cachedBootstrap.Size,
		})
		if err != nil {
			return nil, errors.Wrap(err, "read bootstrap from content store")
		}
		defer ra.Close()

		cr := content.NewReader(ra)
		if err := utils.UnpackFile(cr, utils.BootstrapFileNameInLayer, _parentBootstrapPath); err != nil {
			return nil, errors.Wrap(err, "unpack nydus bootstrap")
		}
	}

	build := builder.New(p.builderPath)
	var chunkDictPath *string
	if chunkDict != nil {
		chunkDictPath = &chunkDict.BootstrapPath
	}
	output, err := build.Run(builder.Option{
		BootstrapDirPath:    bootstrapDir,
		ChunkDictPath:       chunkDictPath,
		BlobDirPath:         blobDir,
		ParentBootstrapPath: parentBootstrapPath,

		DiffLayerPaths:     diffPaths,
		DiffHintLayerPaths: diffHintPaths,
		DiffSkipLayer:      diffSkip,

		OutputJSONPath: outputJSONPath,
		RafsVersion:    p.rafsVersion,
	})
	if err != nil {
		return nil, errors.Wrapf(err, "build layers %v %v", diffPaths, diffHintPaths)
	}

	return output, nil
}

func (p *Packer) Build(ctx context.Context, chunkDict *ChunkDict, layers []Layer) ([]Descriptor, error) {
	var diffSkip *int
	var parent *BuildLayer

	descs := make([]Descriptor, len(layers))

	// Used to wait all umount finish in containerd#mount.WithTempMount, in case
	// of temp mount leak after acceld/accelctl process exits.
	umountEg := errgroup.Group{}

	// Find cache first, to skip layers that have been built.
	buildLayers := []*BuildLayer{}
	for idx := range layers {
		layer := layers[idx]

		// Find the blob/bootstrap layer cache.

		// If err != nil, the cache should be considered miss and the error should
		// be ignored in order not to affect the next workflow.
		cachedBootstrapDesc, cachedBlobDescs, err := layer.GetCache(ctx)
		if err == nil {
			descs[idx] = Descriptor{
				Blobs:     cachedBlobDescs,
				Bootstrap: *cachedBootstrapDesc,
			}
			for _, blob := range cachedBlobDescs {
				p.cachedBlobs[blob.Digest] = &blob
			}
			if parent == nil || diffSkip != nil {
				_idx := idx
				diffSkip = &_idx
			}
		}

		if diffSkip != nil {
			// All cache hit, skip following mount and build.
			if *diffSkip == len(layers)-1 {
				return descs, nil
			}
		}

		buildLayer := BuildLayer{
			Layer:       layer,
			rafsVersion: p.rafsVersion,
			parent:      parent,
			umountEg:    &umountEg,
		}
		parent = &buildLayer

		buildLayers = append(buildLayers, &buildLayer)
	}

	defer func() {
		// Release all layer mounts.
		for idx := range buildLayers {
			buildLayers[idx].umount(ctx)
		}
		if err := umountEg.Wait(); err != nil {
			logrus.WithError(err).Warnf("failed to umount layer")
		}
	}()

	// Mount all source layers.
	mountEg, mountCtx := errgroup.WithContext(ctx)
	for idx := range buildLayers {
		mountEg.Go(func(idx int) func() error {
			return func() error {
				if err := buildLayers[idx].mount(mountCtx); err != nil {
					return errors.Wrap(err, "layer mount")
				}
				return nil
			}
		}(idx))
	}
	if err := mountEg.Wait(); err != nil {
		return nil, errors.Wrap(err, "export all nydus blobs")
	}
	for idx := range buildLayers {
		layer := buildLayers[idx]
		if err := layer.mountWithLower(ctx, p.flatten); err != nil {
			return nil, errors.Wrap(err, "mount with lower layer")
		}
	}

	// Prepare work directory, the blobs and bootstraps of nydus will
	// be written into the directory.
	workDir, cleanup, err := p.prepareWorkdir()
	if err != nil {
		return nil, errors.Wrap(err, "prepare work directory")
	}
	defer func() {
		if err := cleanup(); err != nil {
			logrus.WithError(err).Warnf("failed to cleanup work dir %s", workDir)
		}
	}()

	// Call nydus builder to build, skip the layers before `diffSkip`.
	output, err := p.diffBuild(ctx, workDir, chunkDict, buildLayers, diffSkip)
	if err != nil {
		return nil, errors.Wrap(err, "diff build with nydus")
	}

	// The base is the first index of layer to build after skipping cache.
	base := 0
	if diffSkip != nil {
		base = *diffSkip + 1
	}

	if len(output.Artifacts) <= 0 {
		return nil, fmt.Errorf("can't find valid nydus bootstrap")
	}

	// Export nydus blobs to content store.
	exportEg, ctx := errgroup.WithContext(ctx)
	for idx := range output.Artifacts {
		exportEg.Go(func(idx int) func() error {
			return func() error {
				// Skip to export cached nydus blobs.
				if idx < base {
					return nil
				}

				artifact := output.Artifacts[idx]
				descs[idx].Blobs = make([]ocispec.Descriptor, len(artifact.Blobs))

				for blobIdx := range artifact.Blobs {
					exportEg.Go(func(blobIdx int) func() error {
						return func() error {
							blobID := artifact.Blobs[blobIdx].BlobID

							// Deduplicate the export of blob in chunk dict image.
							if chunkDict != nil {
								if desc, ok := chunkDict.Blobs[blobID]; ok {
									descs[idx].Blobs[blobIdx] = desc
									return nil
								}
							}

							// Deduplicate the export of cached blob.
							blobDigest := digest.NewDigestFromHex(string(digest.SHA256), blobID)
							if desc, ok := p.cachedBlobs[blobDigest]; ok {
								descs[idx].Blobs[blobIdx] = *desc
								return nil
							}

							// Use singleflight to deduplicate the export of same blob.
							blobPath := path.Join(workDir, "blobs", blobID)
							layer := buildLayers[idx]
							_desc, err, _ := p.sg.Do(blobID, func() (interface{}, error) {
								return layer.exportBlob(ctx, blobPath)
							})
							if err != nil {
								return errors.Wrap(err, "export nydus blob")
							}

							desc := _desc.(*ocispec.Descriptor)
							if desc == nil {
								return nil
							}
							descs[idx].Blobs[blobIdx] = *desc

							return nil
						}
					}(blobIdx))
				}

				return nil
			}
		}(idx))
	}

	// Export nydus bootstraps to content store.
	for idx := range output.Artifacts {
		artifact := output.Artifacts[idx]
		bootstrapPath := path.Join(workDir, "bootstraps", artifact.BootstrapName)
		exportEg.Go(func(idx int) func() error {
			return func() error {
				idx := base + idx
				layer := buildLayers[idx]
				desc, err := layer.exportBootstrap(ctx, &p.sg, bootstrapPath)
				if err != nil {
					return errors.Wrap(err, "export nydus blob")
				}
				descs[idx].Bootstrap = *desc
				return nil
			}
		}(idx))
	}

	// Wait to export all nydus blobs/bootstraps.
	if err := exportEg.Wait(); err != nil {
		return nil, errors.Wrap(err, "export all nydus blobs")
	}

	// Set nydus bootstraps/blobs to cache.
	for idx := range output.Artifacts {
		idx := base + idx
		pre := []ocispec.Descriptor{}
		if idx >= 1 {
			pre = descs[idx-1].Blobs
		}
		delta := getDeltaBlobs(pre, descs[idx].Blobs)

		layer := buildLayers[idx]
		if err := layer.SetCache(ctx, descs[idx].Bootstrap, delta); err != nil {
			return nil, errors.Wrap(err, "set nydus bootstrap cache")
		}
	}

	return descs, nil
}
