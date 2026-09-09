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
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/wso2/wso2-coin/operations/transaction-service/internal/middleware"
	"github.com/wso2/wso2-coin/operations/transaction-service/internal/model"
)

const testUserHeader = "X-User-Assertion"

func newTestMux(t *testing.T, repo *fakeRepo) *http.ServeMux {
	t.Helper()
	mux := http.NewServeMux()
	// A zero-value Verifier decodes the end-user token without signature checks,
	// which is sufficient for exercising the handler wiring in tests.
	NewHandler(NewService(repo, newEnc(t)), &middleware.Verifier{}, testUserHeader).RegisterRoutes(mux)
	return mux
}

// userToken builds an unsigned JWT carrying the given email claim, for the
// decode-only test verifier.
func userToken(t *testing.T, email string) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{"sub": "user-1", "email": email})
	signed, err := tok.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}

func TestHandlerListWalletsEmptyReturnsJSONArray(t *testing.T) {
	mux := newTestMux(t, &fakeRepo{})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/wallets", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != "[]" {
		t.Fatalf("body = %q, want [] (not null) for empty result", body)
	}
}

func TestHandlerListWallets(t *testing.T) {
	repo := &fakeRepo{summaries: []WalletSummaryRow{
		{Address: "0xaaa", DefaultWallet: true, CreatedOn: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)},
	}}
	rec := httptest.NewRecorder()
	newTestMux(t, repo).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/wallets", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var got []model.WalletSummary
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := model.WalletSummary{WalletAddress: "0xaaa", DefaultWallet: true, CreatedOn: "2026-01-02T03:04:05Z"}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("wallets = %+v, want [%+v]", got, want)
	}
}

func TestHandlerListWalletAddressesEmptyReturnsJSONArray(t *testing.T) {
	mux := newTestMux(t, &fakeRepo{})
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/wallets/addresses", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != "[]" {
		t.Fatalf("body = %q, want [] (not null) for empty result", body)
	}
}

func newPaymentRepo(t *testing.T) *fakeRepo {
	t.Helper()
	enc := newEnc(t)
	repo := &fakeRepo{
		owners: map[string]string{"0xuserwallet": "user@example.com"},
		byRef:  map[string]*TxnRow{},
	}
	seedWallet(t, enc, repo, "0xuserwallet", "100")
	seedWallet(t, enc, repo, "0xtreasury", "0")
	return repo
}

func paymentRequest() model.PaymentRequest {
	return model.PaymentRequest{
		FromAddress: "0xuserwallet",
		ToAddress:   "0xtreasury",
		Amount:      "12.5",
		Reference:   "order-123",
		Source:      "STOREFRONT",
	}
}

// doPayment issues a POST /payments with the service caller identity in context and
// the given end-user token in the configured header.
func doPayment(t *testing.T, repo *fakeRepo, userTok string, body model.PaymentRequest) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/payments", bytes.NewReader(payload))
	req = req.WithContext(middleware.WithClientID(req.Context(), "client-1"))
	if userTok != "" {
		req.Header.Set(testUserHeader, userTok)
	}
	rec := httptest.NewRecorder()
	newTestMux(t, repo).ServeHTTP(rec, req)
	return rec
}

func TestHandlerPaymentCreated(t *testing.T) {
	rec := doPayment(t, newPaymentRepo(t), userToken(t, "user@example.com"), paymentRequest())

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/transactions/order-123" {
		t.Errorf("Location = %q, want /transactions/order-123", loc)
	}
	var got model.PaymentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := model.PaymentResponse{Reference: "order-123", FromAddress: "0xuserwallet", ToAddress: "0xtreasury", Amount: "12.500000000"}
	if got != want {
		t.Errorf("response = %+v, want %+v", got, want)
	}
}

func TestHandlerPaymentMissingUserToken(t *testing.T) {
	rec := doPayment(t, newPaymentRepo(t), "", paymentRequest())
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestHandlerPaymentNotOwned(t *testing.T) {
	rec := doPayment(t, newPaymentRepo(t), userToken(t, "someone@else.com"), paymentRequest())
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
}

func TestHandlerPaymentInvalidReference(t *testing.T) {
	body := paymentRequest()
	body.Reference = "bad ref!"
	rec := doPayment(t, newPaymentRepo(t), userToken(t, "user@example.com"), body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

func TestHandlerPaymentIdempotentReplay(t *testing.T) {
	repo := newPaymentRepo(t)
	seedExistingTxn(t, newEnc(t), repo, "order-123", "0xuserwallet", "0xtreasury", "12.5")

	rec := doPayment(t, repo, userToken(t, "user@example.com"), paymentRequest())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (idempotent replay); body = %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Errorf("Location = %q, want empty on idempotent replay", loc)
	}
}

func TestHandlerPaymentReferenceConflict(t *testing.T) {
	repo := newPaymentRepo(t)
	seedExistingTxn(t, newEnc(t), repo, "order-123", "0xuserwallet", "0xtreasury", "99")

	rec := doPayment(t, repo, userToken(t, "user@example.com"), paymentRequest())
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

// doWalletsMe issues a GET /wallets/me with the given end-user token in the
// configured header (omitted when userTok is empty).
func doWalletsMe(t *testing.T, repo *fakeRepo, userTok string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/wallets/me", nil)
	if userTok != "" {
		req.Header.Set(testUserHeader, userTok)
	}
	rec := httptest.NewRecorder()
	newTestMux(t, repo).ServeHTTP(rec, req)
	return rec
}

func TestHandlerWalletsMe(t *testing.T) {
	enc := newEnc(t)
	repo := &fakeRepo{byEmail: map[string][]WalletBalanceRow{
		"user@example.com": {encWalletRow(t, enc, "0xdefault", "42.5", true)},
	}}
	rec := doWalletsMe(t, repo, userToken(t, "user@example.com"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var got []model.UserWallet
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := model.UserWallet{WalletAddress: "0xdefault", Balance: "42.500000000", DefaultWallet: true}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("wallets = %+v, want [%+v]", got, want)
	}
}

func TestHandlerWalletsMeEmptyReturnsJSONArray(t *testing.T) {
	rec := doWalletsMe(t, &fakeRepo{}, userToken(t, "user@example.com"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body := strings.TrimSpace(rec.Body.String()); body != "[]" {
		t.Fatalf("body = %q, want [] (not null) for empty result", body)
	}
}

func TestHandlerWalletsMeMissingUserToken(t *testing.T) {
	rec := doWalletsMe(t, &fakeRepo{}, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestHandlerWalletsMeInvalidUserToken(t *testing.T) {
	rec := doWalletsMe(t, &fakeRepo{}, "not-a-jwt")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestHandlerSearchRejectsTooManyAddresses(t *testing.T) {
	addrs := make([]string, maxSearchAddresses+1)
	for i := range addrs {
		addrs[i] = "0xabc"
	}
	payload, err := json.Marshal(model.TransactionSearchRequest{FromAddresses: addrs})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/transactions/search", bytes.NewReader(payload))
	newTestMux(t, &fakeRepo{}).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// doTransfer issues a POST /wallets/master/transfer as the mapped service client.
func doTransfer(t *testing.T, repo *fakeRepo, body model.TransferRequest) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/wallets/master/transfer", bytes.NewReader(payload))
	req = req.WithContext(middleware.WithClientID(req.Context(), "client-1"))
	rec := httptest.NewRecorder()
	newTestMux(t, repo).ServeHTTP(rec, req)
	return rec
}

func newTransferRepo(t *testing.T) *fakeRepo {
	t.Helper()
	enc := newEnc(t)
	repo := &fakeRepo{masters: map[string]string{"client-1": "0xmaster"}, byRef: map[string]*TxnRow{}}
	seedWallet(t, enc, repo, "0xmaster", "100")
	seedWallet(t, enc, repo, "0xuser", "5")
	return repo
}

func TestHandlerTransferCreatedWithCallerReference(t *testing.T) {
	repo := newTransferRepo(t)

	rec := doTransfer(t, repo, model.TransferRequest{ToAddress: "0xuser", Amount: "12", Reference: "QR-7d3f", Source: "CONFERENCE"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "/transactions/QR-7d3f" {
		t.Errorf("Location = %q, want /transactions/QR-7d3f", loc)
	}
	var res model.TransferResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Reference != "QR-7d3f" || res.Amount != "12.000000000" {
		t.Errorf("response = %+v", res)
	}
}

func TestHandlerTransferIdempotentReplay(t *testing.T) {
	repo := newTransferRepo(t)
	seedExistingTxn(t, newEnc(t), repo, "QR-7d3f", "0xmaster", "0xuser", "12")

	rec := doTransfer(t, repo, model.TransferRequest{ToAddress: "0xuser", Amount: "12", Reference: "QR-7d3f"})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (idempotent replay); body = %s", rec.Code, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "" {
		t.Errorf("Location = %q, want empty on idempotent replay", loc)
	}
}

func TestHandlerTransferReferenceConflict(t *testing.T) {
	repo := newTransferRepo(t)
	seedExistingTxn(t, newEnc(t), repo, "QR-7d3f", "0xmaster", "0xuser", "99")

	rec := doTransfer(t, repo, model.TransferRequest{ToAddress: "0xuser", Amount: "12", Reference: "QR-7d3f"})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}
}

func TestHandlerTransferInvalidReference(t *testing.T) {
	rec := doTransfer(t, newTransferRepo(t), model.TransferRequest{ToAddress: "0xuser", Amount: "12", Reference: "bad ref!"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Invalid reference.") {
		t.Errorf("body = %s, want invalid-reference message", rec.Body.String())
	}
}
