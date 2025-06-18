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

	digest "github.com/opencontainers/go-digest"
)

func Test_GetRawBootstrapFromV6(t *testing.T) {
	tests := []struct {
		name      string
		bootstarp string
		sha256    string
		hasError  bool
	}{
		{
			"test_noraml_v6",
			"testdata/v6-image-raw-size-438272.boot",
			"sha256:38a117a42ebfa06b59b6bbd34c023f1b5980eec5387d4cea6b5b7571d5d5b844",
			false,
		},
		{
			"test_invalid_v5",
			"testdata/v5-image.boot",
			"",
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, err := os.Open(tt.bootstarp)
			if err != nil {
				t.Fatalf("open test file %s failed %s", tt.bootstarp, err)
			}
			defer file.Close()
			reader, err := GetRawBootstrapFromV6(file)
			var hasError bool
			if err != nil {
				hasError = true
			}

			if tt.hasError != hasError {
				t.Fatalf("expect hasError equal: %v, but acutal is %v", tt.hasError, hasError)
			}
			if tt.hasError {
				return
			}
			sha, err := digest.FromReader(reader)
			if err != nil {
				t.Fatalf("calculate raw bootstrap digest %s", tt.bootstarp)
			}

			if sha.String() != tt.sha256 {
				t.Fatalf("calculate raw bootstrap digest %s", tt.bootstarp)
			}
		})
	}
}
