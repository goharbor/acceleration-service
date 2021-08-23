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

package daemon

import (
	"github.com/goharbor/acceleration-service/pkg/config"
	"github.com/goharbor/acceleration-service/pkg/handler"
	"github.com/goharbor/acceleration-service/pkg/router"
	"github.com/goharbor/acceleration-service/pkg/server"
)

type Daemon struct {
	server server.Server
}

func NewDaemon(cfg *config.Config) (*Daemon, error) {
	apiHandler, err := handler.NewAPIHandler(cfg)
	if err != nil {
		return nil, err
	}

	router := router.NewLocalRouter(apiHandler)
	srv, err := server.NewHttpServer(&cfg.Server, router)
	if err != nil {
		return nil, err
	}

	return &Daemon{
		server: srv,
	}, nil
}

func (d *Daemon) Run() error {
	return d.server.Run()
}
