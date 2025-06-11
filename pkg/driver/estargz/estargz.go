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

package estargz

import (
	"context"
	"fmt"
	"strconv"

	"github.com/containerd/containerd/v2/core/images/converter"
	"github.com/containerd/platforms"
	"github.com/containerd/stargz-snapshotter/estargz"
	estargzconvert "github.com/containerd/stargz-snapshotter/nativeconverter/estargz"
	zstdchunkedconvert "github.com/containerd/stargz-snapshotter/nativeconverter/zstdchunked"
	"github.com/goharbor/acceleration-service/pkg/content"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

const (
	DefaultCompressFormat = "estargz"
	ZstdChunkedFormat     = "zstdchunked"
)

type layerConverter func([]estargz.Option) converter.ConvertFunc

var compressFormatConverters = map[string]layerConverter{
	DefaultCompressFormat: func(opts []estargz.Option) converter.ConvertFunc {
		return estargzconvert.LayerConvertFunc(opts...)
	},
	ZstdChunkedFormat: func(opts []estargz.Option) converter.ConvertFunc {
		return zstdchunkedconvert.LayerConvertFunc(opts...)
	},
}

type Driver struct {
	cfg        map[string]string
	platformMC platforms.MatchComparer
}

func New(cfg map[string]string, platformMC platforms.MatchComparer) (*Driver, error) {
	return &Driver{cfg, platformMC}, nil
}

func (d *Driver) Convert(ctx context.Context, p content.Provider, ref string) (*ocispec.Descriptor, error) {
	opts, docker2oci, compressFormat, err := getConvertOpts(d.cfg)
	if err != nil {
		return nil, errors.Wrap(err, "parse conversion options")
	}

	layerConv, ok := compressFormatConverters[compressFormat]
	if !ok {
		return nil, fmt.Errorf("unsupported compress format: %s", compressFormat)
	}

	image, err := p.Image(ctx, ref)
	if err != nil {
		return nil, errors.Wrap(err, "get source image")
	}

	return converter.DefaultIndexConvertFunc(layerConv(opts), docker2oci, d.platformMC)(
		ctx, p.ContentStore(), *image)
}

func (d *Driver) Name() string {
	return "estargz"
}

func (d *Driver) Version() string {
	return ""
}

func getConvertOpts(cfg map[string]string) (opts []estargz.Option, docker2oci bool, compressFormat string, err error) {
	compressFormat = DefaultCompressFormat
	if format, ok := cfg["compression"]; ok && format != "" {
		compressFormat = format
	}

	if s, ok := cfg["docker2oci"]; ok {
		b, err := strconv.ParseBool(s)
		if err != nil {
			return nil, false, "", err
		}
		docker2oci = b
	}
	return
}
