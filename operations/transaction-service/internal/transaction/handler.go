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

package transaction

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/wso2/wso2-coin/operations/transaction-service/internal/middleware"
	"github.com/wso2/wso2-coin/operations/transaction-service/internal/model"
	"github.com/wso2/wso2-coin/operations/transaction-service/internal/response"
)

const (
	defaultLimit = 15
	maxLimit     = 100
	// maxSearchAddresses is the maximum number of addresses accepted per filter field.
	maxSearchAddresses = 100
)

// Handler serves the transaction HTTP API.
type Handler struct {
	svc          *Service
	userVerifier *middleware.Verifier
	userHeader   string
}

// NewHandler returns a transaction Handler. userVerifier and userHeader drive the
// payments endpoint's end-user token verification; when userVerifier is nil the
// payments route is not registered (payments disabled).
func NewHandler(svc *Service, userVerifier *middleware.Verifier, userHeader string) *Handler {
	return &Handler{svc: svc, userVerifier: userVerifier, userHeader: userHeader}
}

// RegisterRoutes binds the transaction routes onto the mux. The literal
// /wallets/master/* patterns are more specific than /wallets/{address}/..., so Go's
// ServeMux always routes them there regardless of registration order.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /wallets", h.wallets)
	mux.HandleFunc("GET /wallets/addresses", h.walletAddresses)
	mux.HandleFunc("GET /wallets/master/balance", h.masterBalance)
	mux.HandleFunc("POST /wallets/master/transfer", h.transfer)
	mux.HandleFunc("GET /wallets/{address}/balance", h.walletBalance)
	mux.HandleFunc("POST /transactions/search", h.search)
	mux.HandleFunc("GET /transactions/{reference}", h.transactionByReference)
	if h.userVerifier != nil {
		mux.HandleFunc("POST /payments", h.payments)
		// /wallets/me has two literal segments, so it does not collide with
		// /wallets/addresses, /wallets/{address}/balance or the /wallets/master/* routes.
		mux.HandleFunc("GET /wallets/me", h.walletsMe)
	}
}

func (h *Handler) wallets(w http.ResponseWriter, r *http.Request) {
	wallets, err := h.svc.ListWallets(r.Context())
	if err != nil {
		writeServiceError(r.Context(), w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, wallets)
}

func (h *Handler) walletAddresses(w http.ResponseWriter, r *http.Request) {
	addresses, err := h.svc.ListWalletAddresses(r.Context())
	if err != nil {
		writeServiceError(r.Context(), w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, addresses)
}

func (h *Handler) masterBalance(w http.ResponseWriter, r *http.Request) {
	clientID, ok := caller(w, r)
	if !ok {
		return
	}
	balance, err := h.svc.MasterBalance(r.Context(), clientID)
	if err != nil {
		writeServiceError(r.Context(), w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, balance)
}

func (h *Handler) transfer(w http.ResponseWriter, r *http.Request) {
	clientID, ok := caller(w, r)
	if !ok {
		return
	}
	var req model.TransferRequest
	if err := response.DecodeJSON(w, r, &req); err != nil {
		response.WriteDecodeError(w, err)
		return
	}
	res, err := h.svc.Transfer(r.Context(), clientID, req)
	if err != nil {
		writeServiceError(r.Context(), w, err)
		return
	}
	// Audit line: balances are deliberately never logged.
	slog.InfoContext(r.Context(), "transfer recorded",
		"clientId", clientID,
		"toAddress", res.Response.ToAddress,
		"amount", res.Response.Amount,
		"reference", res.Response.Reference,
		"source", req.Source,
		"idempotent", res.Idempotent)
	if res.Idempotent {
		response.WriteJSON(w, http.StatusOK, res.Response)
		return
	}
	w.Header().Set("Location", "/transactions/"+res.Response.Reference)
	response.WriteJSON(w, http.StatusCreated, res.Response)
}

// payments collects a coin payment from a user-owned wallet into a treasury wallet
// on the user's behalf. The service token (already verified) identifies the calling
// client for the audit log only; the end-user token authorizes the movement.
func (h *Handler) payments(w http.ResponseWriter, r *http.Request) {
	clientID, ok := caller(w, r)
	if !ok {
		return
	}
	email, ok := h.userEmail(w, r)
	if !ok {
		return
	}
	var req model.PaymentRequest
	if err := response.DecodeJSON(w, r, &req); err != nil {
		response.WriteDecodeError(w, err)
		return
	}
	res, err := h.svc.Pay(r.Context(), email, req)
	if err != nil {
		writeServiceError(r.Context(), w, err)
		return
	}
	// Audit line: the raw token, email and balances are deliberately never logged.
	slog.InfoContext(r.Context(), "payment recorded",
		"clientId", clientID,
		"fromAddress", res.Response.FromAddress,
		"toAddress", res.Response.ToAddress,
		"amount", res.Response.Amount,
		"reference", res.Response.Reference,
		"source", req.Source)
	if res.Idempotent {
		response.WriteJSON(w, http.StatusOK, res.Response)
		return
	}
	w.Header().Set("Location", "/transactions/"+res.Response.Reference)
	response.WriteJSON(w, http.StatusCreated, res.Response)
}

// walletsMe returns the caller's own wallets with balances, identified by the verified
// end-user token. The service token authenticates the calling client; the end-user
// token selects whose wallets are listed. Used by the People App to pick a wallet.
func (h *Handler) walletsMe(w http.ResponseWriter, r *http.Request) {
	email, ok := h.userEmail(w, r)
	if !ok {
		return
	}
	wallets, err := h.svc.ListUserWallets(r.Context(), email)
	if err != nil {
		writeServiceError(r.Context(), w, err)
		return
	}
	// User-specific balances at a fixed URL must not be cached by any intermediary.
	w.Header().Set("Cache-Control", "no-store")
	response.WriteJSON(w, http.StatusOK, wallets)
}

// userEmail verifies the end-user token from the configured header and returns its
// trimmed, lowercased email claim. A missing/invalid token or empty email is a 401.
func (h *Handler) userEmail(w http.ResponseWriter, r *http.Request) (string, bool) {
	token := strings.TrimSpace(r.Header.Get(h.userHeader))
	if token == "" {
		response.WriteError(w, http.StatusUnauthorized, response.ErrMsgUnauthorized)
		return "", false
	}
	claims, err := h.userVerifier.Verify(token)
	if err != nil {
		response.WriteError(w, http.StatusUnauthorized, response.ErrMsgUnauthorized)
		return "", false
	}
	email := strings.ToLower(strings.TrimSpace(claims.Email))
	if email == "" {
		response.WriteError(w, http.StatusUnauthorized, response.ErrMsgUnauthorized)
		return "", false
	}
	return email, true
}

func (h *Handler) walletBalance(w http.ResponseWriter, r *http.Request) {
	balance, err := h.svc.WalletBalance(r.Context(), r.PathValue("address"))
	if err != nil {
		writeServiceError(r.Context(), w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, balance)
}

func (h *Handler) search(w http.ResponseWriter, r *http.Request) {
	var req model.TransactionSearchRequest
	if err := response.DecodeJSON(w, r, &req); err != nil {
		response.WriteDecodeError(w, err)
		return
	}
	if len(req.FromAddresses) > maxSearchAddresses || len(req.ToAddresses) > maxSearchAddresses {
		response.WriteError(w, http.StatusBadRequest, response.ErrMsgTooManyAddresses)
		return
	}
	req.Limit = normalizeLimit(req.Limit)
	if req.Offset < 0 {
		req.Offset = 0
	}
	page, err := h.svc.SearchTransactions(r.Context(), req)
	if err != nil {
		writeServiceError(r.Context(), w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, page)
}

func (h *Handler) transactionByReference(w http.ResponseWriter, r *http.Request) {
	txn, err := h.svc.TransactionByReference(r.Context(), r.PathValue("reference"))
	if err != nil {
		writeServiceError(r.Context(), w, err)
		return
	}
	response.WriteJSON(w, http.StatusOK, txn)
}

func caller(w http.ResponseWriter, r *http.Request) (string, bool) {
	clientID := middleware.ClientIDFromContext(r.Context())
	if clientID == "" {
		response.WriteError(w, http.StatusUnauthorized, response.ErrMsgUnauthorized)
		return "", false
	}
	return clientID, true
}

func normalizeLimit(limit int) int {
	if limit <= 0 {
		return defaultLimit
	}
	if limit > maxLimit {
		return maxLimit
	}
	return limit
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
	case errors.Is(err, ErrInvalidReference):
		response.WriteError(w, http.StatusBadRequest, response.ErrMsgInvalidReference)
	case errors.Is(err, ErrInvalidSource):
		response.WriteError(w, http.StatusBadRequest, response.ErrMsgInvalidSource)
	case errors.Is(err, ErrReferenceConflict):
		response.WriteError(w, http.StatusConflict, response.ErrMsgConflict)
	default:
		slog.ErrorContext(ctx, "request failed", "err", err)
		response.WriteError(w, http.StatusInternalServerError, response.ErrMsgInternal)
	}
}
