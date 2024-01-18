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
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAddSuffix(t *testing.T) {
	target, err := addSuffix("192.168.1.1/nginx:test", "-nydus")
	require.NoError(t, err)
	require.Equal(t, "192.168.1.1/nginx:test-nydus", target)

	target, err = addSuffix("192.168.1.1/nginx", "-nydus")
	require.NoError(t, err)
	require.Equal(t, "192.168.1.1/nginx:latest-nydus", target)

	target, err = addSuffix("192.168.1.1/nginx@sha256:f8c20f8bbcb684055b4fea470fdd169c86e87786940b3262335b12ec3adef418", "-nydus")
	require.NoError(t, err)
	require.Equal(t, "192.168.1.1/nginx:latest-nydus", target)

	target, err = addSuffix("192.168.1.1/nginx:test@sha256:f8c20f8bbcb684055b4fea470fdd169c86e87786940b3262335b12ec3adef418", "-nydus")
	require.NoError(t, err)
	require.Equal(t, "192.168.1.1/nginx:test-nydus", target)
}

func TestSetReferenceTag(t *testing.T) {
	cacheRef, err := setReferenceTag("192.168.1.1/nginx:test", "nydus-cache")
	require.NoError(t, err)
	require.Equal(t, "192.168.1.1/nginx:nydus-cache", cacheRef)

	cacheRef, err = setReferenceTag("192.168.1.1/nginx", "nydus-cache")
	require.NoError(t, err)
	require.Equal(t, "192.168.1.1/nginx:nydus-cache", cacheRef)

	cacheRef, err = setReferenceTag("192.168.1.1/nginx@sha256:f8c20f8bbcb684055b4fea470fdd169c86e87786940b3262335b12ec3adef418", "nydus-cache")
	require.NoError(t, err)
	require.Equal(t, "192.168.1.1/nginx:nydus-cache", cacheRef)

	cacheRef, err = setReferenceTag("192.168.1.1/nginx:test@sha256:f8c20f8bbcb684055b4fea470fdd169c86e87786940b3262335b12ec3adef418", "nydus-cache")
	require.NoError(t, err)
	require.Equal(t, "192.168.1.1/nginx:nydus-cache", cacheRef)
}
