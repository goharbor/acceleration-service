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
	"compress/gzip"
	"context"
	"encoding/json"
	"io"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/errdefs"
	nydusify "github.com/containerd/nydus-snapshotter/pkg/converter"
	"github.com/goharbor/acceleration-service/pkg/driver/nydus/utils"
	"github.com/opencontainers/go-digest"
	"github.com/opencontainers/image-spec/identity"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

func mergeNydusLayers(ctx context.Context, cs content.Store, descs []ocispecs.Descriptor, opt nydusify.MergeOption, fsVersion string) (*ocispecs.Descriptor, error) {
	// Extracts nydus bootstrap from nydus format for each layer.
	layers := []nydusify.Layer{}
	blobIDs := []string{}

	var chainID digest.Digest
	for _, blobDesc := range descs {
		ra, err := cs.ReaderAt(ctx, blobDesc)
		if err != nil {
			return nil, errors.Wrapf(err, "get reader for blob %q", blobDesc.Digest)
		}
		defer ra.Close()
		blobIDs = append(blobIDs, blobDesc.Digest.Hex())
		layers = append(layers, nydusify.Layer{
			Digest:   blobDesc.Digest,
			ReaderAt: ra,
		})
		if chainID == "" {
			chainID = identity.ChainID([]digest.Digest{blobDesc.Digest})
		} else {
			chainID = identity.ChainID([]digest.Digest{chainID, blobDesc.Digest})
		}
	}

	// Merge all nydus bootstraps into a final nydus bootstrap.
	pr, pw := io.Pipe()
	go func() {
		defer pw.Close()
		if err := nydusify.Merge(ctx, layers, pw, opt); err != nil {
			pw.CloseWithError(errors.Wrapf(err, "merge nydus bootstrap"))
		}
	}()

	// Compress final nydus bootstrap to tar.gz and write into content store.
	cw, err := content.OpenWriter(ctx, cs, content.WithRef("nydus-merge-"+chainID.String()))
	if err != nil {
		return nil, errors.Wrap(err, "open content store writer")
	}
	defer cw.Close()

	gw := gzip.NewWriter(cw)
	uncompressedDgst := digest.SHA256.Digester()
	compressed := io.MultiWriter(gw, uncompressedDgst.Hash())
	if _, err := io.Copy(compressed, pr); err != nil {
		return nil, errors.Wrapf(err, "copy bootstrap targz into content store")
	}
	if err := gw.Close(); err != nil {
		return nil, errors.Wrap(err, "close gzip writer")
	}

	compressedDgst := cw.Digest()
	if err := cw.Commit(ctx, 0, compressedDgst, content.WithLabels(map[string]string{
		utils.LayerAnnotationUncompressed: uncompressedDgst.Digest().String(),
	})); err != nil {
		if !errdefs.IsAlreadyExists(err) {
			return nil, errors.Wrap(err, "commit to content store")
		}
	}
	if err := cw.Close(); err != nil {
		return nil, errors.Wrap(err, "close content store writer")
	}

	info, err := cs.Info(ctx, compressedDgst)
	if err != nil {
		return nil, errors.Wrap(err, "get info from content store")
	}

	blobIDsBytes, err := json.Marshal(blobIDs)
	if err != nil {
		return nil, errors.Wrap(err, "marshal blob ids")
	}

	desc := ocispecs.Descriptor{
		Digest:    compressedDgst,
		Size:      info.Size,
		MediaType: ocispecs.MediaTypeImageLayerGzip,
		Annotations: map[string]string{
			utils.LayerAnnotationUncompressed:   uncompressedDgst.Digest().String(),
			utils.LayerAnnotationNydusFsVersion: fsVersion,
			// Use this annotation to identify nydus bootstrap layer.
			nydusify.LayerAnnotationNydusBootstrap: "true",
			// Track all blob digests for nydus snapshotter.
			nydusify.LayerAnnotationNydusBlobIDs: string(blobIDsBytes),
		},
	}

	return &desc, nil
}
