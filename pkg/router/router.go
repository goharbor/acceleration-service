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

package router

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/sirupsen/logrus"

	"github.com/goharbor/acceleration-service/pkg/handler"
	"github.com/goharbor/acceleration-service/pkg/server/util"
)

var logger = logrus.WithField("module", "api")

type Router interface {
	Register(server *echo.Echo) error
}

type LocalRouter struct {
	handler handler.Handler
}

func NewLocalRouter(handler handler.Handler) Router {
	return &LocalRouter{
		handler: handler,
	}
}

func (router *LocalRouter) Register(server *echo.Echo) error {
	server.POST("/api/v1/conversions", router.CreateTask)
	server.GET("/api/v1/conversions", router.ListTask)
	server.GET("/api/v1/health", router.CheckHealth)

	// Any unexpected endpoint will return an error.
	server.Any("*", func(ctx echo.Context) error {
		return util.ReplyError(ctx, http.StatusNotFound, errors.New("ERR_INVALID_ENDPOINT"), "Endpoint not found")
	})

	return nil
}
