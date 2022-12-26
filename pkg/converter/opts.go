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

package converter

import (
	"github.com/goharbor/acceleration-service/pkg/content"
)

type ConvertOpts struct {
	provider     content.Provider
	driverType   string
	driverConfig map[string]string
}

type ConvertOpt func(opts *ConvertOpts) error

func WithProvider(provider content.Provider) ConvertOpt {
	return func(opts *ConvertOpts) error {
		opts.provider = provider
		return nil
	}
}

func WithDriver(typ string, config map[string]string) ConvertOpt {
	return func(opts *ConvertOpts) error {
		opts.driverType = typ
		opts.driverConfig = config
		return nil
	}
}
