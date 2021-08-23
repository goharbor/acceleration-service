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

package handler

import (
	"github.com/pkg/errors"

	"github.com/goharbor/acceleration-service/pkg/config"
)

var (
	ErrIllegalParameter = errors.New("ERR_ILLEGAL_PARAMETER")
	ErrUnauthorized     = errors.New("ERR_UNAUTHORIZED")
	ErrNotHealthy       = errors.New("ERR_NOT_HEALTHY")
)

type Handler interface {
	Ping() error
}

type ApiHandler struct {
	config *config.Config
}

func NewAPIHandler(cfg *config.Config) (*ApiHandler, error) {
	h := &ApiHandler{
		config: cfg,
	}

	return h, nil
}

func (handler *ApiHandler) Ping() error {
	return nil
}
