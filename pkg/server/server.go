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

package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/labstack/echo-contrib/prometheus"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/labstack/gommon/log"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/goharbor/acceleration-service/pkg/config"
	"github.com/goharbor/acceleration-service/pkg/errdefs"
	"github.com/goharbor/acceleration-service/pkg/router"
	"github.com/goharbor/acceleration-service/pkg/server/util"
)

const shutdownTimeout = time.Second * 10

var logger = logrus.WithField("module", "api")

type Server interface {
	Run() error
}

type HTTPServer struct {
	addr   string
	server *echo.Echo
}

func NewHTTPServer(cfg *config.ServerConfig, metricCfg *config.MetricConfig, router router.Router) (*HTTPServer, error) {
	server := echo.New()

	server.HideBanner = true
	server.HidePort = true

	// Enforce all meddlewares to use our logger.
	server.Logger.(*log.Logger).SetOutput(logger.Writer())

	// Handle internal error in API handler that is not visible to user.
	server.HTTPErrorHandler = func(err error, ctx echo.Context) {
		if errors.Is(err, echo.ErrUnauthorized) {
			util.ReplyError(
				ctx, http.StatusUnauthorized, errdefs.ErrUnauthorized,
				"Authentication failed, please make sure the `Authorization` header is valid",
			)
			return
		}
		logrus.Error(errors.Wrap(err, fmt.Sprintf("[%s]", cfg.Name)))
		// We do not expose internal error details to user.
		ctx.NoContent(http.StatusInternalServerError)
	}

	// Add logger middleware for HTTP request & response records.
	server.Use(middleware.LoggerWithConfig(middleware.LoggerConfig{
		Format: "${method} ${uri} ${status} ${latency_human} ${bytes_in}>${bytes_out}bytes ${remote_ip}\n",
		Output: logger.Writer(),
		Skipper: func(ctx echo.Context) bool {
			// So many /metrics endpoint requests, so we'd better to ignore the logs.
			return strings.HasPrefix(ctx.Path(), "/metrics")
		},
	}))

	// If any panic occurs in handler, recovery is necessary and an internal
	// error should be returned to client instead of exiting the process.
	server.Use(middleware.RecoverWithConfig(middleware.RecoverConfig{
		// Will affect performance if set this option to false.
		DisableStackAll: true,
		LogLevel:        log.ERROR,
	}))

	if metricCfg.Enabled {
		// Export the metrics of HTTP request/response to `/metrics` endpoint.
		// See https://echo.labstack.com/middleware/prometheus/
		metric := prometheus.NewPrometheus(fmt.Sprintf("acceleration_service_%s", strings.ToLower(cfg.Name)), nil)
		metric.Use(server)
	}

	// Setup UDS if configured
	var addr string
	if len(cfg.Uds) != 0 {
		// Make sure that the previously created socket file is removed,
		// otherwise it will throw the error: "bind: address already in use".
		if err := os.Remove(cfg.Uds); err != nil {
			if !os.IsNotExist(err) {
				logrus.Errorf("fail to remove %s", cfg.Uds)
				return nil, errors.Wrapf(err, "remove unix domain sock %s", cfg.Uds)
			}
		}

		listener, err := net.Listen("unix", cfg.Uds)
		if err != nil {
			logrus.Errorf("fail to listen on %s", cfg.Uds)
			return nil, errors.Wrapf(err, "create listener on %s", cfg.Uds)
		}
		server.Listener = listener
	} else {
		// Otherwise use the provided host/port
		addr = fmt.Sprintf("%s:%s", cfg.Host, cfg.Port)
	}
	// Register all routes to HTTP handler.
	if err := router.Register(server); err != nil {
		return nil, err
	}

	go func() {
		// Wait for interrupt signal to gracefully shutdown the server
		// with a timeout of `shutdownTimeout`.
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, os.Interrupt)
		<-quit
		logrus.Infof("[%s] gracefully shutdowning http server...", cfg.Name)
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			logrus.Fatal(errors.Wrapf(err, fmt.Sprintf("[%s] gracefully shutdown http server", cfg.Name)))
		}
	}()

	logrus.Infof("[%s] HTTP server started on %s%s", cfg.Name, cfg.Uds, addr)

	return &HTTPServer{
		addr:   addr,
		server: server,
	}, nil
}

func (srv *HTTPServer) Run() error {
	if err := srv.server.Start(srv.addr); err != nil {
		if err != http.ErrServerClosed {
			return errors.Wrapf(err, "start http server")
		}
	}

	return nil
}
