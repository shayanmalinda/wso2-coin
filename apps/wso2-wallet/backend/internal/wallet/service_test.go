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
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wso2/wso2-coin/apps/wso2-wallet/backend/internal/crypto"
	"github.com/wso2/wso2-coin/apps/wso2-wallet/backend/internal/model"
	"github.com/wso2/wso2-coin/apps/wso2-wallet/backend/internal/money"
)

type fakeRepo struct {
	wallets []*WalletRow
	txns    []TxnInsert
	grants  []GrantRow
}

func (r *fakeRepo) grantCount(address string) int {
	n := 0
	for _, g := range r.grants {
		if strings.EqualFold(g.Address, address) {
			n++
		}
	}
	return n
}

func (r *fakeRepo) SetWalletName(_ context.Context, address string, encName sql.NullString) error {
	if w := r.find(address); w != nil {
		w.Name = encName
	}
	return nil
}

func (r *fakeRepo) SetWalletNameIfShared(_ context.Context, address, encName string) (bool, error) {
	w := r.find(address)
	if w == nil || r.grantCount(address) == 0 {
		return false, nil
	}
	w.Name = sql.NullString{String: encName, Valid: true}
	return true, nil
}

func (r *fakeRepo) SharedWalletsByEmail(_ context.Context, email string) ([]SharedWalletRow, error) {
	var out []SharedWalletRow
	for _, g := range r.grants {
		if w := r.find(g.Address); w != nil && strings.EqualFold(g.Email, email) {
			cp := *w
			cp.GrantCount = r.grantCount(w.Address)
			out = append(out, SharedWalletRow{Wallet: cp, Grant: g})
		}
	}
	return out, nil
}

func (r *fakeRepo) Grant(_ context.Context, address, email string) (*GrantRow, error) {
	for _, g := range r.grants {
		if strings.EqualFold(g.Address, address) && strings.EqualFold(g.Email, email) {
			cp := g
			return &cp, nil
		}
	}
	return nil, nil
}

func (r *fakeRepo) Grants(_ context.Context, address string) ([]GrantRow, error) {
	var out []GrantRow
	for _, g := range r.grants {
		if strings.EqualFold(g.Address, address) {
			out = append(out, g)
		}
	}
	return out, nil
}

func (r *fakeRepo) UpsertGrant(_ context.Context, g GrantRow) error {
	for i := range r.grants {
		if strings.EqualFold(r.grants[i].Address, g.Address) && strings.EqualFold(r.grants[i].Email, g.Email) {
			r.grants[i] = g
			return nil
		}
	}
	r.grants = append(r.grants, g)
	return nil
}

func (r *fakeRepo) DeleteGrant(_ context.Context, address, email string) (bool, error) {
	for i, g := range r.grants {
		if strings.EqualFold(g.Address, address) && strings.EqualFold(g.Email, email) {
			r.grants = append(r.grants[:i], r.grants[i+1:]...)
			if w := r.find(address); w != nil && r.grantCount(address) == 0 {
				w.Name = sql.NullString{}
			}
			return true, nil
		}
	}
	return false, nil
}

func (r *fakeRepo) find(address string) *WalletRow {
	for _, w := range r.wallets {
		if strings.EqualFold(w.Address, address) {
			return w
		}
	}
	return nil
}

func (r *fakeRepo) WalletsByEmail(_ context.Context, email string) ([]WalletRow, error) {
	var out []WalletRow
	for _, w := range r.wallets {
		if strings.EqualFold(w.Email, email) {
			cp := *w
			cp.GrantCount = r.grantCount(w.Address)
			out = append(out, cp)
		}
	}
	return out, nil
}

func (r *fakeRepo) WalletByAddress(_ context.Context, address string) (*WalletRow, error) {
	w := r.find(address)
	if w == nil {
		return nil, nil
	}
	cp := *w
	cp.GrantCount = r.grantCount(w.Address)
	return &cp, nil
}

func (r *fakeRepo) CountWalletsByEmail(_ context.Context, email string) (int, error) {
	n := 0
	for _, w := range r.wallets {
		if strings.EqualFold(w.Email, email) {
			n++
		}
	}
	return n, nil
}

func (r *fakeRepo) SetPrimary(_ context.Context, email, address string) error {
	for _, w := range r.wallets {
		if strings.EqualFold(w.Email, email) {
			w.DefaultWallet = strings.EqualFold(w.Address, address)
		}
	}
	return nil
}

func (r *fakeRepo) Transactions(_ context.Context, _ string, _, _ int, _ string) ([]TxnRow, error) {
	return nil, nil
}

func (r *fakeRepo) Tx(_ context.Context, fn func(Queries) error) error {
	return fn(&fakeQueries{r: r})
}

type fakeQueries struct {
	r *fakeRepo
}

func (q *fakeQueries) LockGrant(ctx context.Context, address, email string) (*GrantRow, error) {
	return q.r.Grant(ctx, address, email)
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

func (q *fakeQueries) CountWalletsByEmail(_ context.Context, email string) (int, error) {
	return q.r.CountWalletsByEmail(context.Background(), email)
}

func (q *fakeQueries) UpdateBalance(_ context.Context, address, encBalance string) error {
	w := q.r.find(address)
	if w == nil {
		return errors.New("wallet not found")
	}
	w.Balance = sql.NullString{String: encBalance, Valid: true}
	return nil
}

func (q *fakeQueries) InsertWallet(_ context.Context, address, email, encBalance string, isDefault bool) error {
	q.r.wallets = append(q.r.wallets, &WalletRow{
		Address: address, Email: email, DefaultWallet: isDefault,
		Balance: sql.NullString{String: encBalance, Valid: true}, CreatedOn: time.Now(),
	})
	return nil
}

func (q *fakeQueries) InsertTransaction(_ context.Context, t TxnInsert) error {
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

func seed(t *testing.T, enc Encryptor, r *fakeRepo, address, email, amount string, def bool) {
	t.Helper()
	units, err := money.ParseUnits(amount)
	if err != nil {
		t.Fatalf("parse %q: %v", amount, err)
	}
	c, err := enc.Encrypt(money.FormatUnits(units), balanceAAD(address))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	r.wallets = append(r.wallets, &WalletRow{
		Address: address, Email: email, DefaultWallet: def,
		Balance: sql.NullString{String: c, Valid: true}, CreatedOn: time.Now(),
	})
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

func TestCreateWalletWithAllocation(t *testing.T) {
	enc := newEnc(t)
	repo := &fakeRepo{}
	seed(t, enc, repo, "0xfund", "system", "100", false)
	svc := NewService(repo, enc, InitialCoins{Enabled: true, Amount: "10", FundingWallet: "0xfund", EmailDomain: "example.com"}, Sharing{EmailDomain: "example.com"})

	wallet, err := svc.CreateWallet(context.Background(), "alice@example.com")
	if err != nil {
		t.Fatalf("CreateWallet: %v", err)
	}
	if !wallet.DefaultWallet {
		t.Error("first wallet should be default")
	}
	if got := balanceOf(t, enc, repo, wallet.Address); got != "10.000000000" {
		t.Errorf("new wallet balance = %q, want 10.000000000", got)
	}
	if got := balanceOf(t, enc, repo, "0xfund"); got != "90.000000000" {
		t.Errorf("funding balance = %q, want 90.000000000", got)
	}
	if len(repo.txns) != 1 {
		t.Fatalf("recorded %d transactions, want 1", len(repo.txns))
	}
	if repo.txns[0].FromAddress != "0xfund" || repo.txns[0].ToAddress != wallet.Address {
		t.Error("allocation transaction endpoints incorrect")
	}
	if !strings.HasPrefix(repo.txns[0].Reference, "0x") || len(repo.txns[0].Reference) != 66 {
		t.Errorf("reference = %q, want 0x + 64 hex", repo.txns[0].Reference)
	}
	if repo.txns[0].Source != sourceWalletApp {
		t.Errorf("source = %q, want %q", repo.txns[0].Source, sourceWalletApp)
	}
}

func TestCreateWalletNoAllocationForExternalDomain(t *testing.T) {
	enc := newEnc(t)
	repo := &fakeRepo{}
	seed(t, enc, repo, "0xfund", "system", "100", false)
	svc := NewService(repo, enc, InitialCoins{Enabled: true, Amount: "10", FundingWallet: "0xfund", EmailDomain: "example.com"}, Sharing{EmailDomain: "example.com"})

	wallet, err := svc.CreateWallet(context.Background(), "bob@gmail.com")
	if err != nil {
		t.Fatalf("CreateWallet: %v", err)
	}
	if got := balanceOf(t, enc, repo, wallet.Address); got != "0.000000000" {
		t.Errorf("new wallet balance = %q, want 0.000000000", got)
	}
	if len(repo.txns) != 0 {
		t.Errorf("recorded %d transactions, want 0", len(repo.txns))
	}
	if got := balanceOf(t, enc, repo, "0xfund"); got != "100.000000000" {
		t.Errorf("funding balance = %q, want unchanged 100.000000000", got)
	}
}

func TestCreateWalletSecondHasNoAllocation(t *testing.T) {
	enc := newEnc(t)
	repo := &fakeRepo{}
	seed(t, enc, repo, "0xfund", "system", "100", false)
	svc := NewService(repo, enc, InitialCoins{Enabled: true, Amount: "10", FundingWallet: "0xfund", EmailDomain: "example.com"}, Sharing{EmailDomain: "example.com"})

	if _, err := svc.CreateWallet(context.Background(), "alice@example.com"); err != nil {
		t.Fatalf("first CreateWallet: %v", err)
	}
	second, err := svc.CreateWallet(context.Background(), "alice@example.com")
	if err != nil {
		t.Fatalf("second CreateWallet: %v", err)
	}
	if second.DefaultWallet {
		t.Error("second wallet should not be the default")
	}
	if got := balanceOf(t, enc, repo, second.Address); got != "0.000000000" {
		t.Errorf("second wallet balance = %q, want 0.000000000", got)
	}
	if len(repo.txns) != 1 {
		t.Errorf("allocation transactions = %d, want 1", len(repo.txns))
	}
	if got := balanceOf(t, enc, repo, "0xfund"); got != "90.000000000" {
		t.Errorf("funding balance = %q, want 90.000000000 (debited once)", got)
	}
}

func TestTransfer(t *testing.T) {
	enc := newEnc(t)
	repo := &fakeRepo{}
	seed(t, enc, repo, "0xaaa", "alice@example.com", "30", true)
	seed(t, enc, repo, "0xbbb", "bob@example.com", "5", true)
	svc := NewService(repo, enc, InitialCoins{}, Sharing{EmailDomain: "example.com"})

	res, err := svc.Transfer(context.Background(), "alice@example.com",
		model.TransferRequest{FromAddress: "0xaaa", ToAddress: "0xbbb", Amount: "12"})
	if err != nil {
		t.Fatalf("Transfer: %v", err)
	}
	if !strings.HasPrefix(res.TransactionHash, "0x") || len(res.TransactionHash) != 66 {
		t.Errorf("transaction hash = %q, want 0x + 64 hex", res.TransactionHash)
	}
	if got := balanceOf(t, enc, repo, "0xaaa"); got != "18.000000000" {
		t.Errorf("sender balance = %q, want 18.000000000", got)
	}
	if got := balanceOf(t, enc, repo, "0xbbb"); got != "17.000000000" {
		t.Errorf("recipient balance = %q, want 17.000000000", got)
	}
	if len(repo.txns) != 1 || repo.txns[0].Reference != res.TransactionHash {
		t.Error("transfer transaction not recorded with the returned reference")
	}
	if repo.txns[0].Source != sourceWalletApp {
		t.Errorf("source = %q, want %q", repo.txns[0].Source, sourceWalletApp)
	}
}

func TestTransferErrors(t *testing.T) {
	tests := []struct {
		name    string
		caller  string
		req     model.TransferRequest
		wantErr error
	}{
		{"insufficient funds", "alice@example.com", model.TransferRequest{FromAddress: "0xaaa", ToAddress: "0xbbb", Amount: "100"}, ErrInsufficientFunds},
		{"self transfer", "alice@example.com", model.TransferRequest{FromAddress: "0xaaa", ToAddress: "0xaaa", Amount: "1"}, ErrSelfTransfer},
		{"recipient not found", "alice@example.com", model.TransferRequest{FromAddress: "0xaaa", ToAddress: "0xzzz", Amount: "1"}, ErrRecipientNotFound},
		{"not owned", "mallory@example.com", model.TransferRequest{FromAddress: "0xaaa", ToAddress: "0xbbb", Amount: "1"}, ErrForbidden},
		{"invalid amount", "alice@example.com", model.TransferRequest{FromAddress: "0xaaa", ToAddress: "0xbbb", Amount: "abc"}, ErrInvalidAmount},
		{"zero amount", "alice@example.com", model.TransferRequest{FromAddress: "0xaaa", ToAddress: "0xbbb", Amount: "0"}, ErrInvalidAmount},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc := newEnc(t)
			repo := &fakeRepo{}
			seed(t, enc, repo, "0xaaa", "alice@example.com", "30", true)
			seed(t, enc, repo, "0xbbb", "bob@example.com", "5", true)
			svc := NewService(repo, enc, InitialCoins{}, Sharing{EmailDomain: "example.com"})

			_, err := svc.Transfer(context.Background(), tt.caller, tt.req)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Transfer error = %v, want %v", err, tt.wantErr)
			}
			if got := balanceOf(t, enc, repo, "0xaaa"); got != "30.000000000" {
				t.Errorf("sender balance changed on failure: %q", got)
			}
			if got := balanceOf(t, enc, repo, "0xbbb"); got != "5.000000000" {
				t.Errorf("recipient balance changed on failure: %q", got)
			}
		})
	}
}

func TestBalance(t *testing.T) {
	enc := newEnc(t)
	repo := &fakeRepo{}
	seed(t, enc, repo, "0xaaa", "alice@example.com", "30", true)
	svc := NewService(repo, enc, InitialCoins{}, Sharing{EmailDomain: "example.com"})

	bal, err := svc.Balance(context.Background(), "alice@example.com", "0xaaa")
	if err != nil {
		t.Fatalf("Balance: %v", err)
	}
	if bal.Balance != "30.000000000" {
		t.Errorf("balance = %q, want 30.000000000", bal.Balance)
	}

	if _, err := svc.Balance(context.Background(), "mallory@example.com", "0xaaa"); !errors.Is(err, ErrForbidden) {
		t.Errorf("Balance for non-owner error = %v, want ErrForbidden", err)
	}
}
