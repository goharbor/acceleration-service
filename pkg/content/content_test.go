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

	"github.com/stretchr/testify/require"
)

func TestContenSize(t *testing.T) {
	os.MkdirAll("./tmp", 0755)
	defer os.RemoveAll("./tmp")
	content, err := NewContent("./tmp", "./tmp", "1000MB")
	require.NoError(t, err)
	size, err := content.Size()
	require.NoError(t, err)
	require.Equal(t, size, int64(0))
}
