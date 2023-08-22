// Copyright Project Harbor Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package content

import (
	"context"
	"os"
	"testing"

	"github.com/containerd/containerd/content"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

func TestContenSize(t *testing.T) {
	os.MkdirAll("./tmp", 0755)
	defer os.RemoveAll("./tmp")
	content, err := NewContent("./tmp", "./tmp", "1000MB", false, 200, nil)
	require.NoError(t, err)
	size, err := content.Size()
	require.NoError(t, err)
	require.Equal(t, size, int64(0))
}

func TestRemoteCacheFIFO(t *testing.T) {
	os.MkdirAll("./tmp", 0755)
	defer os.RemoveAll("./tmp")
	cs, err := NewContent("./tmp", "./tmp", "1000MB", true, 5, nil)
	require.NoError(t, err)

	// Assume the following layers were already cached in remote cache
	cs.remoteCache.Add("sha256:3ea3641165a4082d1feb7f933b11589a98f6b44906b3ab7224fd57afdb81bd22", ocispec.Descriptor{
		Digest:    digest.Digest("sha256:937705f5a7ed2202ef5efab32f37940bcdc3f3bd8da5d472a605f92a1ee2abb8"),
		MediaType: "application/vnd.oci.image.layer.nydus.blob.v1",
		Size:      28104,
		Annotations: map[string]string{
			"containerd.io/snapshot/nydus-source-digest": "sha256:3ea3641165a4082d1feb7f933b11589a98f6b44906b3ab7224fd57afdb81bd22",
		},
	})
	// This layer will be update by testTargetDesc, with different size.
	cs.remoteCache.Add("sha256:d2a0c55811ee17d810756e8abe1c0e1680b83c1674b167116b5d22c92426271e", ocispec.Descriptor{
		Digest:    digest.Digest("sha256:29de1420e90dc712e218943e2503bb8f8e7e0ea33094d21386fc6dfb6296cdaf"),
		MediaType: "application/vnd.oci.image.layer.nydus.blob.v1",
		Size:      30000000,
		Annotations: map[string]string{
			"containerd.io/snapshot/nydus-source-digest": "sha256:d2a0c55811ee17d810756e8abe1c0e1680b83c1674b167116b5d22c92426271e",
		},
	})
	cs.remoteCache.Add("sha256:61c5a878f9b478eabfb7d805f8b28037bc75fc2723a8ed34a25ed926bb235630", ocispec.Descriptor{
		Digest:    digest.Digest("sha256:5068ad3783f63b82ecf9434161923dec024275c2c09f79f517a8eaeba9765488"),
		MediaType: "application/vnd.oci.image.layer.nydus.blob.v1",
		Size:      4629756,
		Annotations: map[string]string{
			"containerd.io/snapshot/nydus-source-digest": "sha256:61c5a878f9b478eabfb7d805f8b28037bc75fc2723a8ed34a25ed926bb235630",
		},
	})

	// The testTargetDesc is from a manifest, will be update to the remote cache.
	// More closer to the front, more lower. More closer to the back, more upper.
	testTargetDesc := []ocispec.Descriptor{
		{
			Digest:    digest.Digest("sha256:281acccc8425676d6cb1840e2656409f58da7e0e8d4c07f9092d35f9c9810e20"),
			MediaType: "application/vnd.oci.image.layer.nydus.blob.v1",
			Size:      39784900,
			Annotations: map[string]string{
				"containerd.io/snapshot/nydus-source-digest": "sha256:ccc37bca66e7c29e0d65a4279511fe9a93932a4bb80e79e95144f3812632b61a",
			},
		},
		{
			Digest:    digest.Digest("sha256:29de1420e90dc712e218943e2503bb8f8e7e0ea33094d21386fc6dfb6296cdaf"),
			MediaType: "application/vnd.oci.image.layer.nydus.blob.v1",
			Size:      38238606,
			Annotations: map[string]string{
				"containerd.io/snapshot/nydus-source-digest": "sha256:d2a0c55811ee17d810756e8abe1c0e1680b83c1674b167116b5d22c92426271e",
			},
		},
		{
			Digest:    digest.Digest("sha256:a3a8cb24ca266f5d7513f2c8be9a140c9f5abe223e0d80c68024b2ad5b6c928a"),
			MediaType: "application/vnd.oci.image.layer.nydus.blob.v1",
			Size:      26308,
			Annotations: map[string]string{
				"containerd.io/snapshot/nydus-source-digest": "sha256:163d9761f77314ae2beb0cfdb0f86245bef6071233fece6a3e4a3d1d4db23c5f",
			},
		},
		{
			Digest:    digest.Digest("sha256:a9453f674413979bd6cdaaeb4399cd8d9c5449cf7625860e6d93eb2f916b4e50"),
			MediaType: "application/vnd.oci.image.layer.nydus.blob.v1",
			Size:      26315,
			Annotations: map[string]string{
				"containerd.io/snapshot/nydus-source-digest": "sha256:c767afe512614d4adf05b9136a92c89b6151804d41ca92f005ce3af0032c36de",
			},
		},
	}

	// Apdate cache to lru from upper to lower
	ctx := context.Background()
	for i := len(testTargetDesc) - 1; i >= 0; i-- {
		layer := testTargetDesc[i]
		_, err := cs.Update(ctx, content.Info{
			Digest: layer.Digest,
			Size:   layer.Size,
			Labels: layer.Annotations,
		})
		require.NoError(t, err)
	}
	require.Equal(t, cs.remoteCache.Size(), 5)
	require.Equal(t, "sha256:5068ad3783f63b82ecf9434161923dec024275c2c09f79f517a8eaeba9765488", cs.remoteCache.Values()[0].Digest.String())
	require.Equal(t, "sha256:a9453f674413979bd6cdaaeb4399cd8d9c5449cf7625860e6d93eb2f916b4e50", cs.remoteCache.Values()[1].Digest.String())
	require.Equal(t, "sha256:a3a8cb24ca266f5d7513f2c8be9a140c9f5abe223e0d80c68024b2ad5b6c928a", cs.remoteCache.Values()[2].Digest.String())
	require.Equal(t, "sha256:29de1420e90dc712e218943e2503bb8f8e7e0ea33094d21386fc6dfb6296cdaf", cs.remoteCache.Values()[3].Digest.String())
	require.Equal(t, int64(38238606), cs.remoteCache.Values()[3].Size)
	require.Equal(t, "sha256:281acccc8425676d6cb1840e2656409f58da7e0e8d4c07f9092d35f9c9810e20", cs.remoteCache.Values()[4].Digest.String())
}
