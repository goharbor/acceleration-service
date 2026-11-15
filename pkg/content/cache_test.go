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

	"github.com/containerd/containerd/v2/core/leases"
	"github.com/containerd/containerd/v2/pkg/namespaces"
	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

func TestLeaseCache(t *testing.T) {
	lc := newLeaseCache()
	require.NoError(t, lc.Add("test_1", "1"))
	require.NoError(t, lc.Add("test_2", "1"))
	require.NoError(t, lc.Add("test_3", "2"))
	require.NoError(t, lc.Add("test_2", "2"))
	require.Error(t, lc.Add("test_3", "test"))
	require.Equal(t, 3, lc.Len())
	key, err := lc.Remove()
	require.NoError(t, err)
	require.Equal(t, "test_1", key)
	key, err = lc.Remove()
	require.NoError(t, err)
	require.Equal(t, "test_3", key)
	key, err = lc.Remove()
	require.NoError(t, err)
	require.Equal(t, "test_2", key)
}

func TestLeaseCacheInit(t *testing.T) {
	os.MkdirAll("./tmp", 0755)
	defer os.RemoveAll("./tmp")
	content, err := NewContent(nil, "./tmp", "./tmp", "100MB")
	require.NoError(t, err)
	testDigest := []string{
		"sha256:9bb13890319dc01e5f8a4d3d0c4c72685654d682d568350fd38a02b1d70aee6b",
		"sha256:17dc42e40d4af0a9e84c738313109f3a95e598081beef6c18a05abb57337aa5d",
		"sha256:9bb13890319dc01e5f8a4d3d0c4c72685654d682d568350fd38a02b1d70aee6b",
		"sha256:17dc42e40d4af0a9e84c738313109f3a95e598081beef6c18a05abb57337aa5d",
		"sha256:aeb53f8db8c94d2cd63ca860d635af4307967aa11a2fdead98ae0ab3a329f470",
		"sha256:613f4797d2b6653634291a990f3e32378c7cfe3cdd439567b26ca340b8946013",
	}
	for _, digestString := range testDigest {
		require.NoError(t, content.updateLease((*digest.Digest)(&digestString)))
	}
	lc := newLeaseCache()
	require.NoError(t, lc.Init(content.lm))
}

func TestCleanLeasesWhenLeaseCacheIsExhausted(t *testing.T) {
	os.MkdirAll("./tmp-clean", 0755)
	defer os.RemoveAll("./tmp-clean")
	content, err := NewContent(nil, "./tmp-clean", "./tmp-clean", "100MB")
	require.NoError(t, err)

	ctx := namespaces.WithNamespace(context.Background(), accelerationServiceNamespace)
	dgst := "sha256:9bb13890319dc01e5f8a4d3d0c4c72685654d682d568350fd38a02b1d70aee6b"
	_, err = content.lm.Create(ctx, leases.WithID(dgst))
	require.NoError(t, err)

	// a digest reaching Add twice on the same used count leaves Len() above the
	// number of keys actually held, so the guard in cleanLeases does not hold
	require.NoError(t, content.lc.Add(dgst, "1"))
	require.NoError(t, content.lc.Add(dgst, "1"))

	require.Error(t, content.cleanLeases(ctx, 1))
}

func TestLeaseCacheLenCountsEachKeyOnce(t *testing.T) {
	lc := newLeaseCache()
	require.NoError(t, lc.Add("test_1", "1"))
	require.NoError(t, lc.Add("test_1", "1"))
	require.Equal(t, 1, lc.Len())

	_, err := lc.Remove()
	require.NoError(t, err)
	require.Equal(t, 0, lc.Len())
}
