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
	"fmt"
	"io"

	"github.com/containerd/containerd/archive/compression"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/images"
	"github.com/containerd/containerd/images/converter"
	nydusify "github.com/containerd/nydus-snapshotter/pkg/converter"
	"github.com/goharbor/acceleration-service/pkg/driver/nydus/backend"
	"github.com/goharbor/acceleration-service/pkg/driver/nydus/utils"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

func convertToNydusLayer(opt nydusify.PackOption, backend backend.Backend) converter.ConvertFunc {
	return func(ctx context.Context, cs content.Store, desc ocispec.Descriptor) (*ocispec.Descriptor, error) {
		if !images.IsLayerType(desc.MediaType) {
			return nil, nil
		}

		ra, err := cs.ReaderAt(ctx, desc)
		if err != nil {
			return nil, errors.Wrap(err, "get source blob reader")
		}
		defer ra.Close()
		rdr := io.NewSectionReader(ra, 0, ra.Size())

		ref := fmt.Sprintf("convert-nydus-from-%s", desc.Digest)
		dst, err := content.OpenWriter(ctx, cs, content.WithRef(ref))
		if err != nil {
			return nil, errors.Wrap(err, "open blob writer")
		}
		defer dst.Close()

		tr, err := compression.DecompressStream(rdr)
		if err != nil {
			return nil, errors.Wrap(err, "decompress blob stream")
		}

		digester := digest.SHA256.Digester()
		pr, pw := io.Pipe()
		tw, err := nydusify.Pack(ctx, io.MultiWriter(pw, digester.Hash()), opt)
		if err != nil {
			return nil, errors.Wrap(err, "pack tar to nydus")
		}

		go func() {
			defer pw.Close()
			if _, err := io.Copy(tw, tr); err != nil {
				pw.CloseWithError(err)
				return
			}
			if err := tr.Close(); err != nil {
				pw.CloseWithError(err)
				return
			}
			if err := tw.Close(); err != nil {
				pw.CloseWithError(err)
				return
			}
		}()

		if err := content.Copy(ctx, dst, pr, 0, ""); err != nil {
			return nil, errors.Wrap(err, "copy nydus blob to content store")
		}

		blobDigest := digester.Digest()
		info, err := cs.Info(ctx, blobDigest)
		if err != nil {
			return nil, errors.Wrapf(err, "get blob info %s", blobDigest)
		}

		newDesc := ocispec.Descriptor{
			Digest:    blobDigest,
			Size:      info.Size,
			MediaType: utils.MediaTypeNydusBlob,
			Annotations: map[string]string{
				// Use `containerd.io/uncompressed` to generate DiffID of
				// layer defined in OCI spec.
				utils.LayerAnnotationUncompressed: blobDigest.String(),
				utils.LayerAnnotationNydusBlob:    "true",
			},
		}

		if backend != nil {
			blobRa, err := cs.ReaderAt(ctx, newDesc)
			if err != nil {
				return nil, errors.Wrap(err, "get nydus blob reader")
			}
			blobReader := io.NewSectionReader(blobRa, 0, blobRa.Size())

			if err := backend.Push(ctx, blobReader, blobDigest); err != nil {
				return nil, errors.Wrap(err, "push to storage backend")
			}
		}

		return &newDesc, nil
	}
}
