// Copyright (c) 2026 WSO2 LLC. (https://www.wso2.com).
//
// WSO2 LLC. licenses this file to you under the Apache License,
// Version 2.0 (the "License"); you may not use this file except
// in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

package people

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/wso2/wso2-coin/apps/wso2-wallet/backend/internal/middleware"
	"github.com/wso2/wso2-coin/apps/wso2-wallet/backend/internal/response"
)

// Handler serves the employee search HTTP API.
type Handler struct {
	dir *Directory
}

// NewHandler returns an employee search Handler. dir is nil when the HR entity is
// not configured; searches then report the directory as unavailable.
func NewHandler(dir *Directory) *Handler {
	return &Handler{dir: dir}
}

// RegisterRoutes binds the employee search route onto the mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/employees", h.search)
}

func (h *Handler) search(w http.ResponseWriter, r *http.Request) {
	user := middleware.UserInfoFromContext(r.Context())
	if user == nil || user.Email == "" {
		response.WriteError(w, http.StatusUnauthorized, response.ErrMsgUnauthorized)
		return
	}
	if h.dir == nil {
		response.WriteError(w, http.StatusServiceUnavailable, response.ErrMsgDirectoryUnavailable)
		return
	}
	// The directory is for people in the organization only.
	if !h.dir.InCompany(user.Email) {
		response.WriteError(w, http.StatusForbidden, response.ErrMsgForbidden)
		return
	}
	employees, err := h.dir.Search(r.Context(), r.URL.Query().Get("q"), user.Email)
	if err != nil {
		if !errors.Is(err, ErrUnavailable) {
			slog.ErrorContext(r.Context(), "employee search failed", "err", err)
		}
		response.WriteError(w, http.StatusServiceUnavailable, response.ErrMsgDirectoryUnavailable)
		return
	}
	// Results are personal data and change with the directory; do not cache them.
	w.Header().Set("Cache-Control", "no-store")
	response.WriteJSON(w, http.StatusOK, employees)
}
