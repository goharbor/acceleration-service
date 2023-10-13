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
	"os"
	"testing"

	"github.com/opencontainers/go-digest"
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
