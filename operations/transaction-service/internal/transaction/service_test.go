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
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wso2/wso2-coin/operations/transaction-service/internal/crypto"
	"github.com/wso2/wso2-coin/operations/transaction-service/internal/model"
	"github.com/wso2/wso2-coin/operations/transaction-service/internal/money"
)

type fakeRepo struct {
	masters     map[string]string
	owners      map[string]string
	wallets     []*WalletRow
	txns        []TxnInsert
	searchRows  []TxnRow
	searchTotal int
	lastFilters SearchFilters
	byRef       map[string]*TxnRow
	summaries   []WalletSummaryRow
	addresses   []string
	byEmail     map[string][]WalletBalanceRow
}

func (r *fakeRepo) find(address string) *WalletRow {
	for _, w := range r.wallets {
		if strings.EqualFold(w.Address, address) {
			return w
		}
	}
	return nil
}

func (r *fakeRepo) MasterWalletByClient(_ context.Context, clientID string) (string, error) {
	addr, ok := r.masters[clientID]
	if !ok {
		return "", ErrNoMasterWallet
	}
	return addr, nil
}

func (r *fakeRepo) WalletByAddress(_ context.Context, address string) (*WalletRow, error) {
	w := r.find(address)
	if w == nil {
		return nil, nil
	}
	cp := *w
	return &cp, nil
}

func (r *fakeRepo) WalletOwnedBy(_ context.Context, address, email string) (bool, error) {
	owner, ok := r.owners[address]
	return ok && strings.EqualFold(owner, email), nil
}

func (r *fakeRepo) WalletsByEmail(_ context.Context, email string) ([]WalletBalanceRow, error) {
	return r.byEmail[email], nil
}

func (r *fakeRepo) ListWallets(_ context.Context) ([]WalletSummaryRow, error) {
	return r.summaries, nil
}

func (r *fakeRepo) ListWalletAddresses(_ context.Context) ([]string, error) {
	return r.addresses, nil
}

func (r *fakeRepo) SearchTransactions(_ context.Context, f SearchFilters) ([]TxnRow, int, error) {
	r.lastFilters = f
	return r.searchRows, r.searchTotal, nil
}

func (r *fakeRepo) TransactionByReference(_ context.Context, reference string) (*TxnRow, error) {
	t, ok := r.byRef[reference]
	if !ok {
		return nil, nil
	}
	cp := *t
	return &cp, nil
}

func (r *fakeRepo) Tx(_ context.Context, fn func(Queries) error) error {
	// Snapshot wallet balances so a failing closure rolls back, mimicking a real
	// database transaction.
	snapshot := make(map[string]sql.NullString, len(r.wallets))
	for _, w := range r.wallets {
		snapshot[w.Address] = w.Balance
	}
	if err := fn(&fakeQueries{r: r}); err != nil {
		for _, w := range r.wallets {
			w.Balance = snapshot[w.Address]
		}
		return err
	}
	return nil
}

type fakeQueries struct {
	r *fakeRepo
}

func (q *fakeQueries) LockBalance(_ context.Context, address string) (string, error) {
	w := q.r.find(address)
	if w == nil {
		return "", errors.New("wallet not found")
	}
	if !w.Balance.Valid {
		return "", errors.New("no balance")
	}
	return w.Balance.String, nil
}

func (q *fakeQueries) UpdateBalance(_ context.Context, address, encBalance string) error {
	w := q.r.find(address)
	if w == nil {
		return errors.New("wallet not found")
	}
	w.Balance = sql.NullString{String: encBalance, Valid: true}
	return nil
}

func (q *fakeQueries) InsertTransaction(_ context.Context, t TxnInsert) error {
	// Simulate the uq_reference unique index: a reference already recorded (seeded
	// via byRef, or inserted earlier) collides.
	if _, exists := q.r.byRef[t.Reference]; exists {
		return ErrDuplicateReference
	}
	for _, existing := range q.r.txns {
		if existing.Reference == t.Reference {
			return ErrDuplicateReference
		}
	}
	q.r.txns = append(q.r.txns, t)
	return nil
}

func newEnc(t *testing.T) *crypto.Encryptor {
	t.Helper()
	key := make([]byte, crypto.KeySize)
	for i := range key {
		key[i] = byte(i)
	}
	enc, err := crypto.New(key)
	if err != nil {
		t.Fatalf("crypto.New: %v", err)
	}
	return enc
}

func seedWallet(t *testing.T, enc Encryptor, r *fakeRepo, address, amount string) {
	t.Helper()
	units, err := money.ParseUnits(amount)
	if err != nil {
		t.Fatalf("parse %q: %v", amount, err)
	}
	c, err := enc.Encrypt(money.FormatUnits(units), balanceAAD(address))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	r.wallets = append(r.wallets, &WalletRow{Address: address, Balance: sql.NullString{String: c, Valid: true}})
}

func balanceOf(t *testing.T, enc Encryptor, r *fakeRepo, address string) string {
	t.Helper()
	w := r.find(address)
	if w == nil || !w.Balance.Valid {
		t.Fatalf("wallet %s has no balance", address)
	}
	plain, err := enc.Decrypt(w.Balance.String, balanceAAD(address))
	if err != nil {
		t.Fatalf("decrypt balance of %s: %v", address, err)
	}
	return plain
}

func newFixture(t *testing.T) (*crypto.Encryptor, *fakeRepo, *Service) {
	t.Helper()
	enc := newEnc(t)
	repo := &fakeRepo{masters: map[string]string{"client-1": "0xmaster"}}
	seedWallet(t, enc, repo, "0xmaster", "100")
	seedWallet(t, enc, repo, "0xuser", "5")
	return enc, repo, NewService(repo, enc)
}

func TestTransfer(t *testing.T) {
	enc, repo, svc := newFixture(t)

	result, err := svc.Transfer(context.Background(), "client-1",
		model.TransferRequest{ToAddress: "0xuser", Amount: "12"})
	if err != nil {
		t.Fatalf("Transfer: %v", err)
	}
	if result.Idempotent {
		t.Error("a first transfer must not be reported as idempotent")
	}
	res := result.Response
	if res.FromAddress != "0xmaster" || res.ToAddress != "0xuser" {
		t.Errorf("transfer endpoints = %s -> %s, want 0xmaster -> 0xuser", res.FromAddress, res.ToAddress)
	}
	if res.Amount != "12.000000000" {
		t.Errorf("amount = %q, want 12.000000000", res.Amount)
	}
	if !strings.HasPrefix(res.Reference, "0x") || len(res.Reference) != 66 {
		t.Errorf("reference = %q, want 0x + 64 hex", res.Reference)
	}
	if got := balanceOf(t, enc, repo, "0xmaster"); got != "88.000000000" {
		t.Errorf("sender balance = %q, want 88.000000000", got)
	}
	if got := balanceOf(t, enc, repo, "0xuser"); got != "17.000000000" {
		t.Errorf("recipient balance = %q, want 17.000000000", got)
	}
	if len(repo.txns) != 1 || repo.txns[0].Reference != res.Reference {
		t.Error("transfer transaction not recorded with the returned reference")
	}
	if repo.txns[0].Source != "" {
		t.Errorf("source = %q, want empty when not supplied", repo.txns[0].Source)
	}
}

func TestTransferWithCallerReference(t *testing.T) {
	enc, repo, svc := newFixture(t)

	result, err := svc.Transfer(context.Background(), "client-1",
		model.TransferRequest{ToAddress: "0xuser", Amount: "12", Reference: "QR-7d3f", Source: "CONFERENCE"})
	if err != nil {
		t.Fatalf("Transfer: %v", err)
	}
	if result.Idempotent {
		t.Error("a first transfer must not be reported as idempotent")
	}
	if result.Response.Reference != "QR-7d3f" {
		t.Errorf("reference = %q, want the caller-supplied QR-7d3f", result.Response.Reference)
	}
	if len(repo.txns) != 1 || repo.txns[0].Reference != "QR-7d3f" || repo.txns[0].Source != "CONFERENCE" {
		t.Errorf("recorded txns = %+v, want one with reference QR-7d3f and source CONFERENCE", repo.txns)
	}
	if got := balanceOf(t, enc, repo, "0xmaster"); got != "88.000000000" {
		t.Errorf("sender balance = %q, want 88.000000000", got)
	}
}

func TestTransferIdempotentReplay(t *testing.T) {
	enc, repo, svc := newFixture(t)
	repo.byRef = map[string]*TxnRow{}
	seedExistingTxn(t, enc, repo, "QR-7d3f", "0xmaster", "0xuser", "12")

	result, err := svc.Transfer(context.Background(), "client-1",
		model.TransferRequest{ToAddress: "0xuser", Amount: "12", Reference: "QR-7d3f"})
	if err != nil {
		t.Fatalf("Transfer: %v", err)
	}
	if !result.Idempotent {
		t.Error("retry of an identical reference should be idempotent")
	}
	if result.Response.Amount != "12.000000000" || result.Response.ToAddress != "0xuser" {
		t.Errorf("replayed response = %+v", result.Response)
	}
	// Balances must not move and no second transaction may be recorded.
	if got := balanceOf(t, enc, repo, "0xmaster"); got != "100.000000000" {
		t.Errorf("sender balance moved on replay: %q", got)
	}
	if got := balanceOf(t, enc, repo, "0xuser"); got != "5.000000000" {
		t.Errorf("recipient balance moved on replay: %q", got)
	}
	if len(repo.txns) != 0 {
		t.Errorf("replay inserted %d new transactions, want 0", len(repo.txns))
	}
}

func TestTransferReferenceConflict(t *testing.T) {
	enc, repo, svc := newFixture(t)
	repo.byRef = map[string]*TxnRow{}
	// Same reference already used, but for a different amount.
	seedExistingTxn(t, enc, repo, "QR-7d3f", "0xmaster", "0xuser", "99")

	_, err := svc.Transfer(context.Background(), "client-1",
		model.TransferRequest{ToAddress: "0xuser", Amount: "12", Reference: "QR-7d3f"})
	if !errors.Is(err, ErrReferenceConflict) {
		t.Fatalf("Transfer error = %v, want ErrReferenceConflict", err)
	}
	if got := balanceOf(t, enc, repo, "0xmaster"); got != "100.000000000" {
		t.Errorf("sender balance moved on conflict: %q", got)
	}
}

func TestTransferErrors(t *testing.T) {
	tests := []struct {
		name     string
		clientID string
		req      model.TransferRequest
		wantErr  error
	}{
		{"no master mapping", "unknown", model.TransferRequest{ToAddress: "0xuser", Amount: "1"}, ErrForbidden},
		{"recipient not found", "client-1", model.TransferRequest{ToAddress: "0xzzz", Amount: "1"}, ErrRecipientNotFound},
		{"insufficient funds", "client-1", model.TransferRequest{ToAddress: "0xuser", Amount: "1000"}, ErrInsufficientFunds},
		{"self transfer", "client-1", model.TransferRequest{ToAddress: "0xmaster", Amount: "1"}, ErrSelfTransfer},
		{"invalid amount", "client-1", model.TransferRequest{ToAddress: "0xuser", Amount: "abc"}, ErrInvalidAmount},
		{"zero amount", "client-1", model.TransferRequest{ToAddress: "0xuser", Amount: "0"}, ErrInvalidAmount},
		{"invalid reference", "client-1", model.TransferRequest{ToAddress: "0xuser", Amount: "1", Reference: "bad ref!"}, ErrInvalidReference},
		{"invalid source", "client-1", model.TransferRequest{ToAddress: "0xuser", Amount: "1", Source: "conference"}, ErrInvalidSource},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc, repo, svc := newFixture(t)

			_, err := svc.Transfer(context.Background(), tt.clientID, tt.req)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Transfer error = %v, want %v", err, tt.wantErr)
			}
			if got := balanceOf(t, enc, repo, "0xmaster"); got != "100.000000000" {
				t.Errorf("sender balance changed on failure: %q", got)
			}
			if got := balanceOf(t, enc, repo, "0xuser"); got != "5.000000000" {
				t.Errorf("recipient balance changed on failure: %q", got)
			}
			if len(repo.txns) != 0 {
				t.Errorf("recorded %d transactions on failure, want 0", len(repo.txns))
			}
		})
	}
}

func newPaymentFixture(t *testing.T) (*crypto.Encryptor, *fakeRepo, *Service) {
	t.Helper()
	enc := newEnc(t)
	repo := &fakeRepo{
		owners: map[string]string{"0xuserwallet": "user@example.com"},
		byRef:  map[string]*TxnRow{},
	}
	seedWallet(t, enc, repo, "0xuserwallet", "100")
	seedWallet(t, enc, repo, "0xtreasury", "0")
	return enc, repo, NewService(repo, enc)
}

func validPaymentRequest() model.PaymentRequest {
	return model.PaymentRequest{
		FromAddress: "0xuserwallet",
		ToAddress:   "0xtreasury",
		Amount:      "12.5",
		Reference:   "order-123",
		Source:      "STOREFRONT",
	}
}

func TestPay(t *testing.T) {
	enc, repo, svc := newPaymentFixture(t)

	res, err := svc.Pay(context.Background(), "user@example.com", validPaymentRequest())
	if err != nil {
		t.Fatalf("Pay: %v", err)
	}
	if res.Idempotent {
		t.Error("first Pay should not be an idempotent replay")
	}
	if res.Response.FromAddress != "0xuserwallet" || res.Response.ToAddress != "0xtreasury" {
		t.Errorf("endpoints = %s -> %s, want 0xuserwallet -> 0xtreasury", res.Response.FromAddress, res.Response.ToAddress)
	}
	if res.Response.Amount != "12.500000000" {
		t.Errorf("amount = %q, want 12.500000000", res.Response.Amount)
	}
	if res.Response.Reference != "order-123" {
		t.Errorf("reference = %q, want order-123", res.Response.Reference)
	}
	if got := balanceOf(t, enc, repo, "0xuserwallet"); got != "87.500000000" {
		t.Errorf("sender balance = %q, want 87.500000000", got)
	}
	if got := balanceOf(t, enc, repo, "0xtreasury"); got != "12.500000000" {
		t.Errorf("recipient balance = %q, want 12.500000000", got)
	}
	if len(repo.txns) != 1 || repo.txns[0].Reference != "order-123" || repo.txns[0].Source != "STOREFRONT" {
		t.Errorf("payment not recorded with reference and source: %+v", repo.txns)
	}
}

func TestPayErrors(t *testing.T) {
	req := validPaymentRequest
	tests := []struct {
		name    string
		email   string
		req     model.PaymentRequest
		wantErr error
	}{
		{"invalid reference chars", "user@example.com", withRef(req(), "bad ref!"), ErrInvalidReference},
		{"reference too long", "user@example.com", withRef(req(), "a"+strings.Repeat("b", 66)), ErrInvalidReference},
		{"invalid source lowercase", "user@example.com", withSource(req(), "storefront"), ErrInvalidSource},
		{"invalid source too short", "user@example.com", withSource(req(), "A"), ErrInvalidSource},
		{"invalid amount", "user@example.com", withAmount(req(), "abc"), ErrInvalidAmount},
		{"zero amount", "user@example.com", withAmount(req(), "0"), ErrInvalidAmount},
		{"self transfer", "user@example.com", withTo(req(), "0xuserwallet"), ErrSelfTransfer},
		{"not owned", "someone@else.com", req(), ErrForbidden},
		{"recipient not found", "user@example.com", withTo(req(), "0xmissing"), ErrRecipientNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc, repo, svc := newPaymentFixture(t)

			_, err := svc.Pay(context.Background(), tt.email, tt.req)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Pay error = %v, want %v", err, tt.wantErr)
			}
			if got := balanceOf(t, enc, repo, "0xuserwallet"); got != "100.000000000" {
				t.Errorf("sender balance changed on failure: %q", got)
			}
			if got := balanceOf(t, enc, repo, "0xtreasury"); got != "0.000000000" {
				t.Errorf("recipient balance changed on failure: %q", got)
			}
			if len(repo.txns) != 0 {
				t.Errorf("recorded %d transactions on failure, want 0", len(repo.txns))
			}
		})
	}
}

func TestPayAcceptsHexReference(t *testing.T) {
	_, _, svc := newPaymentFixture(t)
	ref := "0x" + strings.Repeat("a", 64)

	res, err := svc.Pay(context.Background(), "user@example.com", withRef(validPaymentRequest(), ref))
	if err != nil {
		t.Fatalf("Pay with 0x+64hex reference = %v, want nil", err)
	}
	if res.Response.Reference != ref {
		t.Errorf("reference = %q, want %q", res.Response.Reference, ref)
	}
}

func TestPayIdempotentReplay(t *testing.T) {
	enc, repo, svc := newPaymentFixture(t)
	seedExistingTxn(t, enc, repo, "order-123", "0xuserwallet", "0xtreasury", "12.5")

	res, err := svc.Pay(context.Background(), "user@example.com", validPaymentRequest())
	if err != nil {
		t.Fatalf("Pay: %v", err)
	}
	if !res.Idempotent {
		t.Error("replay of an identical reference should be idempotent")
	}
	if res.Response.Amount != "12.500000000" {
		t.Errorf("amount = %q, want 12.500000000", res.Response.Amount)
	}
	// The balances must not move on an idempotent replay.
	if got := balanceOf(t, enc, repo, "0xuserwallet"); got != "100.000000000" {
		t.Errorf("sender balance moved on replay: %q", got)
	}
	if got := balanceOf(t, enc, repo, "0xtreasury"); got != "0.000000000" {
		t.Errorf("recipient balance moved on replay: %q", got)
	}
	if len(repo.txns) != 0 {
		t.Errorf("replay inserted %d new transactions, want 0", len(repo.txns))
	}
}

func TestPayReferenceConflict(t *testing.T) {
	enc, repo, svc := newPaymentFixture(t)
	// Same reference already used, but for a different amount.
	seedExistingTxn(t, enc, repo, "order-123", "0xuserwallet", "0xtreasury", "99")

	_, err := svc.Pay(context.Background(), "user@example.com", validPaymentRequest())
	if !errors.Is(err, ErrReferenceConflict) {
		t.Fatalf("Pay error = %v, want ErrReferenceConflict", err)
	}
	if got := balanceOf(t, enc, repo, "0xuserwallet"); got != "100.000000000" {
		t.Errorf("sender balance moved on conflict: %q", got)
	}
}

func withRef(r model.PaymentRequest, ref string) model.PaymentRequest  { r.Reference = ref; return r }
func withSource(r model.PaymentRequest, s string) model.PaymentRequest { r.Source = s; return r }
func withAmount(r model.PaymentRequest, a string) model.PaymentRequest { r.Amount = a; return r }
func withTo(r model.PaymentRequest, to string) model.PaymentRequest    { r.ToAddress = to; return r }

func seedExistingTxn(t *testing.T, enc Encryptor, r *fakeRepo, ref, from, to, amount string) {
	t.Helper()
	units, err := money.ParseUnits(amount)
	if err != nil {
		t.Fatalf("parse %q: %v", amount, err)
	}
	c, err := enc.Encrypt(money.FormatUnits(units), amountAADForRef(ref))
	if err != nil {
		t.Fatalf("encrypt amount: %v", err)
	}
	r.byRef[ref] = &TxnRow{
		Reference:   sql.NullString{String: ref, Valid: true},
		FromAddress: from,
		ToAddress:   to,
		Amount:      c,
		CreatedOn:   time.Now().UTC(),
	}
}

func TestMasterBalance(t *testing.T) {
	_, _, svc := newFixture(t)

	bal, err := svc.MasterBalance(context.Background(), "client-1")
	if err != nil {
		t.Fatalf("MasterBalance: %v", err)
	}
	if bal.WalletAddress != "0xmaster" || bal.Balance != "100.000000000" {
		t.Errorf("master balance = %+v, want {0xmaster 100.000000000}", bal)
	}

	if _, err := svc.MasterBalance(context.Background(), "unknown"); !errors.Is(err, ErrForbidden) {
		t.Errorf("MasterBalance for unmapped client error = %v, want ErrForbidden", err)
	}
}

func TestWalletBalance(t *testing.T) {
	_, _, svc := newFixture(t)

	bal, err := svc.WalletBalance(context.Background(), "0xuser")
	if err != nil {
		t.Fatalf("WalletBalance: %v", err)
	}
	if bal.Balance != "5.000000000" {
		t.Errorf("balance = %q, want 5.000000000", bal.Balance)
	}

	if _, err := svc.WalletBalance(context.Background(), "0xnope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("WalletBalance for missing wallet error = %v, want ErrNotFound", err)
	}
}

func TestListWallets(t *testing.T) {
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	tests := []struct {
		name string
		rows []WalletSummaryRow
		want []model.WalletSummary
	}{
		{
			name: "empty",
			rows: nil,
			want: []model.WalletSummary{},
		},
		{
			name: "maps rows and formats created_on as RFC3339",
			rows: []WalletSummaryRow{
				{Address: "0xaaa", DefaultWallet: true, CreatedOn: created},
				{Address: "0xbbb", DefaultWallet: false, CreatedOn: created.Add(-time.Hour)},
			},
			want: []model.WalletSummary{
				{WalletAddress: "0xaaa", DefaultWallet: true, CreatedOn: "2026-01-02T03:04:05Z"},
				{WalletAddress: "0xbbb", DefaultWallet: false, CreatedOn: "2026-01-02T02:04:05Z"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(&fakeRepo{summaries: tt.rows}, newEnc(t))

			got, err := svc.ListWallets(context.Background())
			if err != nil {
				t.Fatalf("ListWallets: %v", err)
			}
			if got == nil {
				t.Fatal("ListWallets returned nil; want non-nil slice so the JSON is [] not null")
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d wallets, want %d", len(got), len(tt.want))
			}
			for i, w := range got {
				if w != tt.want[i] {
					t.Errorf("wallet[%d] = %+v, want %+v", i, w, tt.want[i])
				}
			}
		})
	}
}

func TestListWalletAddresses(t *testing.T) {
	tests := []struct {
		name string
		rows []string
		want []string
	}{
		{"empty returns non-nil slice", nil, []string{}},
		{"passes addresses through", []string{"0xaaa", "0xbbb"}, []string{"0xaaa", "0xbbb"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewService(&fakeRepo{addresses: tt.rows}, newEnc(t))

			got, err := svc.ListWalletAddresses(context.Background())
			if err != nil {
				t.Fatalf("ListWalletAddresses: %v", err)
			}
			if got == nil {
				t.Fatal("got nil slice, want non-nil")
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d addresses, want %d", len(got), len(tt.want))
			}
			for i, a := range got {
				if a != tt.want[i] {
					t.Errorf("address[%d] = %q, want %q", i, a, tt.want[i])
				}
			}
		})
	}
}

// encWalletRow builds a WalletBalanceRow whose balance is encrypted under the
// address's balance AAD, mirroring how a real row is stored.
func encWalletRow(t *testing.T, enc Encryptor, address, amount string, defaultWallet bool) WalletBalanceRow {
	t.Helper()
	units, err := money.ParseUnits(amount)
	if err != nil {
		t.Fatalf("parse %q: %v", amount, err)
	}
	c, err := enc.Encrypt(money.FormatUnits(units), balanceAAD(address))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	return WalletBalanceRow{
		Address:       address,
		Balance:       sql.NullString{String: c, Valid: true},
		DefaultWallet: defaultWallet,
	}
}

func TestListUserWallets(t *testing.T) {
	enc := newEnc(t)
	tests := []struct {
		name  string
		email string
		rows  []WalletBalanceRow
		want  []model.UserWallet
	}{
		{
			name:  "no wallets returns empty non-nil slice",
			email: "nobody@example.com",
			rows:  nil,
			want:  []model.UserWallet{},
		},
		{
			name:  "maps rows and decrypts balances in repository order",
			email: "user@example.com",
			rows: []WalletBalanceRow{
				encWalletRow(t, enc, "0xdefault", "42.5", true),
				encWalletRow(t, enc, "0xother", "0", false),
			},
			want: []model.UserWallet{
				{WalletAddress: "0xdefault", Balance: "42.500000000", DefaultWallet: true},
				{WalletAddress: "0xother", Balance: "0.000000000", DefaultWallet: false},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeRepo{byEmail: map[string][]WalletBalanceRow{tt.email: tt.rows}}
			svc := NewService(repo, enc)

			got, err := svc.ListUserWallets(context.Background(), tt.email)
			if err != nil {
				t.Fatalf("ListUserWallets: %v", err)
			}
			if got == nil {
				t.Fatal("ListUserWallets returned nil; want non-nil slice so the JSON is [] not null")
			}
			if len(got) != len(tt.want) {
				t.Fatalf("got %d wallets, want %d", len(got), len(tt.want))
			}
			for i, w := range got {
				if w != tt.want[i] {
					t.Errorf("wallet[%d] = %+v, want %+v", i, w, tt.want[i])
				}
			}
		})
	}
}

// TestListUserWalletsDecryptError verifies that a user_wallet row whose stored
// balance fails to decrypt makes ListUserWallets fail with ErrIntegrity rather
// than returning a partial or zeroed balance. The row's ciphertext is encrypted
// under a different address's AAD, so decrypting it under the row's own address
// fails the integrity check (the decryptBalance ErrIntegrity path).
func TestListUserWalletsDecryptError(t *testing.T) {
	enc := newEnc(t)
	row := encWalletRow(t, enc, "0xother", "42.5", true)
	row.Address = "0xtampered"

	email := "user@example.com"
	repo := &fakeRepo{byEmail: map[string][]WalletBalanceRow{email: {row}}}
	svc := NewService(repo, enc)

	got, err := svc.ListUserWallets(context.Background(), email)
	if !errors.Is(err, ErrIntegrity) {
		t.Fatalf("ListUserWallets error = %v, want ErrIntegrity", err)
	}
	if got != nil {
		t.Errorf("got %+v on integrity failure, want nil (no partial results)", got)
	}
}

func TestSearchTransactions(t *testing.T) {
	enc := newEnc(t)
	repo := &fakeRepo{}
	svc := NewService(repo, enc)

	mkRow := func(ref, amount string, ts time.Time) TxnRow {
		c, err := enc.Encrypt(amount, amountAADForRef(ref))
		if err != nil {
			t.Fatalf("encrypt amount: %v", err)
		}
		return TxnRow{
			Reference:   sql.NullString{String: ref, Valid: true},
			FromAddress: "0xmaster",
			ToAddress:   "0xuser",
			Amount:      c,
			CreatedOn:   ts,
		}
	}
	now := time.Now().UTC()
	repo.searchRows = []TxnRow{
		mkRow("0xref1", "10.000000000", now),
		mkRow("0xref2", "20.000000000", now.Add(-time.Minute)),
	}
	repo.searchTotal = 5

	page, err := svc.SearchTransactions(context.Background(), model.TransactionSearchRequest{
		FromAddresses: []string{"0xmaster"},
		Limit:         2,
		Offset:        0,
	})
	if err != nil {
		t.Fatalf("SearchTransactions: %v", err)
	}
	if page.Total != 5 || page.Limit != 2 || page.Offset != 0 {
		t.Errorf("page meta = {total %d limit %d offset %d}, want {5 2 0}", page.Total, page.Limit, page.Offset)
	}
	if len(page.Transactions) != 2 {
		t.Fatalf("returned %d transactions, want 2", len(page.Transactions))
	}
	if page.Transactions[0].Reference != "0xref1" || page.Transactions[0].Amount != "10.000000000" {
		t.Errorf("first txn = %+v, want ref 0xref1 amount 10.000000000", page.Transactions[0])
	}
	if page.Next == nil || *page.Next != "?offset=2&limit=2" {
		t.Errorf("next = %v, want ?offset=2&limit=2", page.Next)
	}
	if page.Previous != nil {
		t.Errorf("previous = %v, want nil", page.Previous)
	}
	if len(repo.lastFilters.FromAddresses) != 1 || repo.lastFilters.FromAddresses[0] != "0xmaster" {
		t.Errorf("filters not forwarded: %+v", repo.lastFilters)
	}
}
