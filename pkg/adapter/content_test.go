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

package adapter

import (
	"context"
	"os"
	"testing"

	"github.com/containerd/containerd/content/local"
	"github.com/containerd/containerd/metadata"
	"github.com/goharbor/acceleration-service/pkg/config"
	"github.com/stretchr/testify/require"
	bolt "go.etcd.io/bbolt"
)

func TestContenSize(t *testing.T) {
	os.MkdirAll("./tmp", 0755)
	defer os.RemoveAll("./tmp")
	store, err := local.NewStore("./tmp")
	require.NoError(t, err)
	bdb, err := bolt.Open("./tmp/metadata.db", 0655, nil)
	require.NoError(t, err)
	db := metadata.NewDB(bdb, store, nil)
	require.NoError(t, db.Init(context.Background()))
	cfg := config.Config{
		Provider: config.ProviderConfig{
			GCPolicy: config.GCPolicy{
				Threshold: "1000MB",
			},
		},
	}
	content, err := NewContent(db, &cfg)
	require.NoError(t, err)
	size, err := content.Size()
	require.NoError(t, err)
	require.Equal(t, size, int64(0))
}
