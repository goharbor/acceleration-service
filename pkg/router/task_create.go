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
	"net/http"
	"net/url"
	"strconv"

	"github.com/goharbor/acceleration-service/pkg/model"
	"github.com/labstack/echo/v4"

	"github.com/goharbor/acceleration-service/pkg/errdefs"
	"github.com/goharbor/acceleration-service/pkg/server/util"
)

func (r *LocalRouter) CreateTask(ctx echo.Context) error {
	logger.Infof("received webhook request from %s", ctx.Request().RemoteAddr)

	sync, _ := strconv.ParseBool(ctx.QueryParam("sync"))

	payload := new(model.Payload)
	if err := ctx.Bind(payload); err != nil {
		logger.Errorf("invalid webhook payload")
		return util.ReplyError(
			ctx, http.StatusBadRequest, errdefs.ErrIllegalParameter,
			"invalid webhook payload",
		)
	}

	if payload.Type != model.TopicPushArtifact {
		logger.Warnf("unsupported payload type %s", payload.Type)
		return ctx.JSON(http.StatusOK, "Ok")
	}

	auth := ctx.Request().Header.Get(echo.HeaderAuthorization)
	for _, res := range payload.EventData.Resources {
		url, err := url.Parse("dummy://" + res.ResourceURL)
		if err != nil {
			logger.Errorf("failed to parse resource url %s", res.ResourceURL)
			return util.ReplyError(
				ctx, http.StatusBadRequest, errdefs.ErrIllegalParameter,
				"failed to parse resource url",
			)
		}

		if err := r.handler.Auth(ctx.Request().Context(), url.Host, auth); err != nil {
			logger.WithError(err).Errorf("failed to authenticate for host %s", url.Host)
			return util.ReplyError(
				ctx, http.StatusUnauthorized, errdefs.ErrUnauthorized,
				"invalid auth config",
			)
		}
	}

	for _, res := range payload.EventData.Resources {
		if err := r.handler.Convert(ctx.Request().Context(), res.ResourceURL, sync); err != nil {
			return util.ReplyError(
				ctx, http.StatusInternalServerError, errdefs.ErrConvertFailed,
				err.Error(),
			)
		}
	}

	return ctx.JSON(http.StatusOK, "Ok")
}
