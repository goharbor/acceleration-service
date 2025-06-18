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

	"github.com/goharbor/acceleration-service/pkg/errdefs"
	"github.com/goharbor/acceleration-service/pkg/server/util"
	echo "github.com/labstack/echo/v4"
)

func (r *LocalRouter) CheckHealth(ctx echo.Context) error {
	err := r.handler.CheckHealth(ctx.Request().Context())
	if err != nil {
		return util.ReplyError(
			ctx, http.StatusInternalServerError, errdefs.ErrUnhealthy,
			err.Error(),
		)
	}
	return ctx.String(http.StatusOK, "Ok")
}
