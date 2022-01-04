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
	"os"

	"github.com/pkg/errors"

	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/platforms"
	digest "github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/identity"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/goharbor/acceleration-service/pkg/content"
	"github.com/goharbor/acceleration-service/pkg/driver/nydus/export"
	"github.com/goharbor/acceleration-service/pkg/driver/nydus/packer"
)

type Driver struct {
	packer *packer.Packer
}

func New(cfg map[string]string) (*Driver, error) {
	workDir := cfg["work_dir"]
	if workDir == "" {
		workDir = os.TempDir()
	}

	builderPath := cfg["builder"]
	if builderPath == "" {
		builderPath = "nydus-image"
	}

	p, err := packer.New(workDir, builderPath)
	if err != nil {
		return nil, errors.Wrap(err, "create nydus packer")
	}

	return &Driver{
		packer: p,
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

	sourceLayers := []packer.SourceLayer{}

	var chain []digest.Digest
	for idx := range sourceManifest.Layers {
		chain = append(chain, diffIDs[idx])
		upper := identity.ChainID(chain).String()

		sourceLayers = append(sourceLayers, &buildLayer{
			chainID: upper,
			sn:      content.Snapshotter(),
			cs:      content.ContentStore(),
		})
	}

	nydusLayers, err := nydus.packer.Build(ctx, sourceLayers)
	if err != nil {
		return nil, errors.Wrap(err, "build nydus image")
	}

	desc, err := export.Export(ctx, content, nydusLayers)
	if err != nil {
		return nil, errors.Wrap(err, "export nydus manifest")
	}

	return desc, nil
}
