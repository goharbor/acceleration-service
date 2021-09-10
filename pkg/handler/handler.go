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
	"context"
	"fmt"

	"github.com/pkg/errors"

	"github.com/goharbor/acceleration-service/pkg/config"
	"github.com/goharbor/acceleration-service/pkg/converter"
)

// Handler for handling HTTP requests.
type Handler interface {
	// Auth checks if the auth header in webhook request for
	// the specified host is correct.
	Auth(ctx context.Context, host string, authHeader string) error
	// Convert converts source image to target image by specifying
	// source image reference, the conversion is asynchronous, and
	// if the sync option is specified, the HTTP request will be
	// blocked until the conversion is complete.
	Convert(ctx context.Context, ref string, sync bool) error
}

type LocalHandler struct {
	cfg *config.Config
	cvt converter.Converter
}

func NewLocalHandler(cfg *config.Config) (*LocalHandler, error) {
	cvt, err := converter.NewLocalConverter(cfg)
	if err != nil {
		return nil, errors.Wrap(err, "create converter")
	}

	handler := &LocalHandler{
		cfg: cfg,
		cvt: cvt,
	}

	return handler, nil
}

func (handler *LocalHandler) Auth(ctx context.Context, host string, authHeader string) error {
	if authHeader != "" {
		source, ok := handler.cfg.Provider.Source[host]
		if !ok {
			return fmt.Errorf("not found config for host %s", host)
		}
		if authHeader != source.Webhook.AuthHeader {
			return fmt.Errorf("unmatched auth header for host %s", host)
		}
	}
	return nil
}

func (handler *LocalHandler) Convert(ctx context.Context, ref string, sync bool) error {
	return handler.cvt.Dispatch(ctx, ref, sync)
}
