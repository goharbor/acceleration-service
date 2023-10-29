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

package utils

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPackTargzInfo(t *testing.T) {
	file, err := os.CreateTemp("", "nydus-driver-archive-test")
	assert.Nil(t, err)
	defer os.RemoveAll(file.Name())

	err = os.WriteFile(file.Name(), make([]byte, 1024*200), 0666)
	assert.Nil(t, err)

	digest, size, err := PackTargzInfo(file.Name(), "test", true)
	assert.Nil(t, err)

	assert.Equal(t, "sha256:6cdd1b26d54d5852fbea95a81cbb25383975b70b4ffad9f9b6d25c7a434a51eb", digest.String())
	assert.Equal(t, size, int64(315))
}
