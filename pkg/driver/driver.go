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

package driver

import (
	"context"
	"fmt"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/goharbor/acceleration-service/pkg/config"
	"github.com/goharbor/acceleration-service/pkg/content"
	"github.com/goharbor/acceleration-service/pkg/driver/estargz"
	"github.com/goharbor/acceleration-service/pkg/driver/nydus"
)

// Driver defines image conversion interface, the following
// methods need to be implemented by image format providers.
type Driver interface {
	// Convert converts the source image to target image, where
	// content parameter provides necessary image utils, image
	// content store and so on. If conversion successful, the
	// converted image manifest will be returned, otherwise a
	// non-nil error will be returned.
	Convert(context.Context, content.Provider) (*ocispec.Descriptor, error)
}

func NewLocalDriver(cfg *config.DriverConfig) (Driver, error) {
	switch cfg.Type {
	case "nydus":
		return nydus.New(cfg.Config)
	case "estargz":
		return estargz.New(cfg.Config)
	default:
		return nil, fmt.Errorf("unsupported driver %s", cfg.Type)
	}
}
