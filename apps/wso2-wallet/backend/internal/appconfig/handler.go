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

// Package appconfig serves client-facing runtime configuration.
package appconfig

import (
	"net/http"
	"strings"

	"github.com/wso2/wso2-coin/apps/wso2-wallet/backend/internal/middleware"
	"github.com/wso2/wso2-coin/apps/wso2-wallet/backend/internal/model"
	"github.com/wso2/wso2-coin/apps/wso2-wallet/backend/internal/response"
)

// Handler serves the app configuration HTTP API.
type Handler struct {
	maintenanceMode    bool
	maintenanceMessage string
	companyDomain      string
}

// NewHandler returns an app-config Handler over the given maintenance settings.
// companyDomain decides who is offered wallet sharing.
func NewHandler(maintenanceMode bool, maintenanceMessage, companyDomain string) *Handler {
	return &Handler{
		maintenanceMode:    maintenanceMode,
		maintenanceMessage: maintenanceMessage,
		companyDomain:      strings.ToLower(strings.TrimSpace(companyDomain)),
	}
}

// RegisterRoutes binds the app-config routes onto the mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/app-config", h.appConfig)
}

func (h *Handler) appConfig(w http.ResponseWriter, r *http.Request) {
	response.WriteJSON(w, http.StatusOK, model.AppConfig{
		MaintenanceMode:      h.maintenanceMode,
		MaintenanceMessage:   h.maintenanceMessage,
		WalletSharingEnabled: h.inCompany(r),
	})
}

// inCompany reports whether the caller belongs to the organization. Sharing is
// limited to it, so the app hides sharing from everyone else. This only drives what
// is shown; the wallet service enforces the rule when a grant is made.
func (h *Handler) inCompany(r *http.Request) bool {
	user := middleware.UserInfoFromContext(r.Context())
	if user == nil || h.companyDomain == "" {
		return false
	}
	return strings.HasSuffix(strings.ToLower(strings.TrimSpace(user.Email)), "@"+h.companyDomain)
}
