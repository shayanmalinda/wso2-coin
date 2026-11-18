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

package wallet

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/wso2/wso2-coin/apps/wso2-wallet/backend/internal/middleware"
	"github.com/wso2/wso2-coin/apps/wso2-wallet/backend/internal/model"
	"github.com/wso2/wso2-coin/apps/wso2-wallet/backend/internal/response"
)

const (
	defaultLimit = 15
	maxLimit     = 100
)

// Handler serves the wallet HTTP API.
type Handler struct {
	svc *Service
}

// NewHandler returns a wallet Handler.
func NewHandler(svc *Service) *Handler {
	return &Handler{svc: svc}
}

// RegisterRoutes binds the wallet routes onto the mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/wallets", h.listWallets)
	mux.HandleFunc("POST /api/v1/wallets", h.createWallet)
	mux.HandleFunc("POST /api/v1/wallets/{address}/set-primary", h.setPrimary)
	mux.HandleFunc("GET /api/v1/wallets/{address}/balance", h.balance)
	mux.HandleFunc("GET /api/v1/wallets/{address}/transactions", h.transactions)
	mux.HandleFunc("GET /api/v1/wallets/{address}/health", h.walletHealth)
	mux.HandleFunc("GET /api/v1/wallets/{address}/info", h.walletInfo)
	mux.HandleFunc("POST /api/v1/wallets/{address}/name", h.rename)
	mux.HandleFunc("GET /api/v1/wallets/{address}/access", h.listAccess)
	mux.HandleFunc("POST /api/v1/wallets/{address}/access", h.grantAccess)
	mux.HandleFunc("POST /api/v1/wallets/{address}/access/revoke", h.revokeAccess)
	mux.HandleFunc("POST /api/v1/wallets/{address}/access/leave", h.leaveWallet)
	mux.HandleFunc("POST /api/v1/transfers", h.transfer)
}

func (h *Handler) listWallets(w http.ResponseWriter, r *http.Request) {
	email, ok := caller(w, r)
	if !ok {
		return
	}
	wallets, err := h.svc.ListWallets(r.Context(), email)
	if err != nil {
		writeServiceError(r.Context(), w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, wallets)
}

func (h *Handler) createWallet(w http.ResponseWriter, r *http.Request) {
	email, ok := caller(w, r)
	if !ok {
		return
	}
	wallet, err := h.svc.CreateWallet(r.Context(), email)
	if err != nil {
		writeServiceError(r.Context(), w, err)
		return
	}
	response.WriteJSON(w, http.StatusCreated, wallet)
}

func (h *Handler) setPrimary(w http.ResponseWriter, r *http.Request) {
	email, ok := caller(w, r)
	if !ok {
		return
	}
	if err := h.svc.SetPrimary(r.Context(), email, r.PathValue("address")); err != nil {
		writeServiceError(r.Context(), w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) balance(w http.ResponseWriter, r *http.Request) {
	email, ok := caller(w, r)
	if !ok {
		return
	}
	balance, err := h.svc.Balance(r.Context(), email, r.PathValue("address"))
	if err != nil {
		writeServiceError(r.Context(), w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, balance)
}

func (h *Handler) transactions(w http.ResponseWriter, r *http.Request) {
	email, ok := caller(w, r)
	if !ok {
		return
	}
	limit, offset, direction := paginationParams(r)
	page, err := h.svc.Transactions(r.Context(), email, r.PathValue("address"), limit, offset, direction)
	if err != nil {
		writeServiceError(r.Context(), w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, page)
}

func (h *Handler) walletHealth(w http.ResponseWriter, r *http.Request) {
	email, ok := caller(w, r)
	if !ok {
		return
	}
	health, err := h.svc.WalletHealth(r.Context(), email, r.PathValue("address"))
	if err != nil {
		writeServiceError(r.Context(), w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, health)
}

func (h *Handler) walletInfo(w http.ResponseWriter, r *http.Request) {
	if _, ok := caller(w, r); !ok {
		return
	}
	info, err := h.svc.WalletInfo(r.Context(), r.PathValue("address"))
	if err != nil {
		writeServiceError(r.Context(), w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, info)
}

func (h *Handler) rename(w http.ResponseWriter, r *http.Request) {
	email, ok := caller(w, r)
	if !ok {
		return
	}
	var req model.RenameRequest
	if err := response.DecodeJSON(w, r, &req); err != nil {
		response.WriteDecodeError(w, err)
		return
	}
	if req.Name == nil {
		response.WriteError(w, http.StatusBadRequest, response.ErrMsgBadRequest)
		return
	}
	if err := h.svc.Rename(r.Context(), email, r.PathValue("address"), *req.Name); err != nil {
		writeServiceError(r.Context(), w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listAccess(w http.ResponseWriter, r *http.Request) {
	email, ok := caller(w, r)
	if !ok {
		return
	}
	grants, err := h.svc.ListAccess(r.Context(), email, r.PathValue("address"))
	if err != nil {
		writeServiceError(r.Context(), w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, grants)
}

func (h *Handler) grantAccess(w http.ResponseWriter, r *http.Request) {
	email, ok := caller(w, r)
	if !ok {
		return
	}
	var req model.AccessRequest
	if err := response.DecodeJSON(w, r, &req); err != nil {
		response.WriteDecodeError(w, err)
		return
	}
	if err := h.svc.GrantAccess(r.Context(), email, r.PathValue("address"), req); err != nil {
		writeServiceError(r.Context(), w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) revokeAccess(w http.ResponseWriter, r *http.Request) {
	email, ok := caller(w, r)
	if !ok {
		return
	}
	var req model.RevokeRequest
	if err := response.DecodeJSON(w, r, &req); err != nil {
		response.WriteDecodeError(w, err)
		return
	}
	if err := h.svc.RevokeAccess(r.Context(), email, r.PathValue("address"), req.Email); err != nil {
		writeServiceError(r.Context(), w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// leaveWallet removes the caller's own access to a wallet shared with them.
func (h *Handler) leaveWallet(w http.ResponseWriter, r *http.Request) {
	email, ok := caller(w, r)
	if !ok {
		return
	}
	if err := h.svc.LeaveWallet(r.Context(), email, r.PathValue("address")); err != nil {
		writeServiceError(r.Context(), w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) transfer(w http.ResponseWriter, r *http.Request) {
	email, ok := caller(w, r)
	if !ok {
		return
	}
	var req model.TransferRequest
	if err := response.DecodeJSON(w, r, &req); err != nil {
		response.WriteDecodeError(w, err)
		return
	}
	res, err := h.svc.Transfer(r.Context(), email, req)
	if err != nil {
		writeServiceError(r.Context(), w, err)
		return
	}
	response.WriteJSON(w, http.StatusCreated, res)
}

func caller(w http.ResponseWriter, r *http.Request) (string, bool) {
	user := middleware.UserInfoFromContext(r.Context())
	if user == nil || user.Email == "" {
		response.WriteError(w, http.StatusUnauthorized, response.ErrMsgUnauthorized)
		return "", false
	}
	return user.Email, true
}

func paginationParams(r *http.Request) (limit, offset int, direction string) {
	limit = defaultLimit
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		limit = v
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	if v, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && v > 0 {
		offset = v
	}
	switch r.URL.Query().Get("direction") {
	case "sent":
		direction = "sent"
	case "received":
		direction = "received"
	}
	return limit, offset, direction
}

func writeServiceError(ctx context.Context, w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		response.WriteError(w, http.StatusNotFound, response.ErrMsgNotFound)
	case errors.Is(err, ErrForbidden):
		response.WriteError(w, http.StatusForbidden, response.ErrMsgForbidden)
	case errors.Is(err, ErrRecipientNotFound):
		response.WriteError(w, http.StatusBadRequest, response.ErrMsgRecipientNotFound)
	case errors.Is(err, ErrSelfTransfer):
		response.WriteError(w, http.StatusBadRequest, response.ErrMsgSelfTransfer)
	case errors.Is(err, ErrInvalidAmount):
		response.WriteError(w, http.StatusBadRequest, response.ErrMsgInvalidAmount)
	case errors.Is(err, ErrInsufficientFunds):
		response.WriteError(w, http.StatusBadRequest, response.ErrMsgInsufficientFunds)
	case errors.Is(err, ErrInvalidName):
		response.WriteError(w, http.StatusBadRequest, response.ErrMsgInvalidName)
	case errors.Is(err, ErrNameNotAllowed):
		response.WriteError(w, http.StatusBadRequest, response.ErrMsgNameNotAllowed)
	case errors.Is(err, ErrInvalidGrant):
		response.WriteError(w, http.StatusBadRequest, response.ErrMsgInvalidGrant)
	case errors.Is(err, ErrDomainNotAllowed):
		response.WriteError(w, http.StatusBadRequest, response.ErrMsgDomainNotAllowed)
	default:
		slog.ErrorContext(ctx, "request failed", "err", err)
		response.WriteError(w, http.StatusInternalServerError, response.ErrMsgInternal)
	}
}
