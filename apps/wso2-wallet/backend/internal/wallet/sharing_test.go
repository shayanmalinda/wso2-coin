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

	"github.com/wso2/wso2-coin/apps/wso2-wallet/backend/internal/model"
)

const (
	owner  = "owner@wso2.com"
	member = "member@wso2.com"
	team   = "0xteam"
)

func sharingSvc(t *testing.T) (*Service, *fakeRepo, Encryptor) {
	t.Helper()
	enc := newEnc(t)
	repo := &fakeRepo{}
	seed(t, enc, repo, team, owner, "100", true)
	seed(t, enc, repo, "0xother", "other@wso2.com", "0", true)
	return NewService(repo, enc, InitialCoins{}, Sharing{EmailDomain: "wso2.com"}), repo, enc
}

func grant(t *testing.T, svc *Service, email string, level model.Role) {
	t.Helper()
	if err := svc.GrantAccess(context.Background(), owner, team, model.AccessRequest{Email: email, AccessLevel: level}); err != nil {
		t.Fatalf("GrantAccess(%s, %s): %v", email, level, err)
	}
}

func TestGrantAccessRules(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name   string
		caller string
		req    model.AccessRequest
		want   error
	}{
		{"read grant", owner, model.AccessRequest{Email: member, AccessLevel: model.RoleRead}, nil},
		{"email is normalized", owner, model.AccessRequest{Email: "  Member@WSO2.com ", AccessLevel: model.RoleTransact}, nil},
		{"non-owner cannot grant", member, model.AccessRequest{Email: "x@wso2.com", AccessLevel: model.RoleRead}, ErrForbidden},
		{"outside the organization", owner, model.AccessRequest{Email: "x@gmail.com", AccessLevel: model.RoleRead}, ErrDomainNotAllowed},
		{"lookalike domain", owner, model.AccessRequest{Email: "x@notwso2.com", AccessLevel: model.RoleRead}, ErrDomainNotAllowed},
		{"owner level cannot be granted", owner, model.AccessRequest{Email: member, AccessLevel: model.RoleOwner}, ErrInvalidGrant},
		{"owner cannot grant to self", owner, model.AccessRequest{Email: owner, AccessLevel: model.RoleRead}, ErrInvalidGrant},
		{"malformed email", owner, model.AccessRequest{Email: "not-an-email", AccessLevel: model.RoleRead}, ErrInvalidGrant},
		{"look-alike letter in email", owner, model.AccessRequest{Email: "memb\u00e9r@wso2.com", AccessLevel: model.RoleRead}, ErrInvalidGrant},
		{"email too long", owner, model.AccessRequest{Email: strings.Repeat("a", 250) + "@wso2.com", AccessLevel: model.RoleRead}, ErrInvalidGrant},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, _ := sharingSvc(t)
			if err := svc.GrantAccess(ctx, tc.caller, team, tc.req); !errors.Is(err, tc.want) {
				t.Errorf("GrantAccess = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestReadMemberCanViewButNotTransfer(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := sharingSvc(t)
	grant(t, svc, member, model.RoleRead)

	if _, err := svc.Balance(ctx, member, team); err != nil {
		t.Errorf("Balance: %v", err)
	}
	if _, err := svc.Transactions(ctx, member, team, 10, 0, ""); err != nil {
		t.Errorf("Transactions: %v", err)
	}
	_, err := svc.Transfer(ctx, member, model.TransferRequest{FromAddress: team, ToAddress: "0xother", Amount: "1"})
	if !errors.Is(err, ErrForbidden) {
		t.Errorf("Transfer = %v, want ErrForbidden", err)
	}
}

func TestTransactMemberTransferRecordsInitiator(t *testing.T) {
	ctx := context.Background()
	svc, repo, enc := sharingSvc(t)
	grant(t, svc, member, model.RoleTransact)

	if _, err := svc.Transfer(ctx, member, model.TransferRequest{FromAddress: team, ToAddress: "0xother", Amount: "40"}); err != nil {
		t.Fatalf("Transfer: %v", err)
	}
	if got := balanceOf(t, enc, repo, team); got != "60.000000000" {
		t.Errorf("team balance = %q, want 60.000000000", got)
	}
	txn := repo.txns[len(repo.txns)-1]
	got, err := enc.Decrypt(txn.InitiatedBy, initiatorAAD(txn.Reference))
	if err != nil || got != member {
		t.Errorf("initiator = %q, %v; want %q", got, err, member)
	}
}

func TestInitiatorShownOnlyToSendingWallet(t *testing.T) {
	ctx := context.Background()
	svc, _, enc := sharingSvc(t)
	sealed, err := enc.Encrypt(member, initiatorAAD("0xref"))
	if err != nil {
		t.Fatal(err)
	}
	row := TxnRow{
		Reference:   sql.NullString{String: "0xref", Valid: true},
		InitiatedBy: sql.NullString{String: sealed, Valid: true},
	}
	if got := svc.initiator(ctx, row, model.DirectionSent); got != member {
		t.Errorf("sent side initiator = %q, want %q", got, member)
	}
	if got := svc.initiator(ctx, row, model.DirectionReceived); got != "" {
		t.Errorf("received side initiator = %q, want empty", got)
	}
	row.Reference.String = "0xanother"
	if got := svc.initiator(ctx, row, model.DirectionSent); got != "" {
		t.Errorf("initiator moved to another transaction = %q, want empty", got)
	}
}

func TestMembersCannotManageWallet(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := sharingSvc(t)
	grant(t, svc, member, model.RoleTransact)

	if err := svc.Rename(ctx, member, team, "HR"); !errors.Is(err, ErrForbidden) {
		t.Errorf("Rename = %v, want ErrForbidden", err)
	}
	if err := svc.SetPrimary(ctx, member, team); !errors.Is(err, ErrForbidden) {
		t.Errorf("SetPrimary = %v, want ErrForbidden", err)
	}
	if _, err := svc.ListAccess(ctx, member, team); !errors.Is(err, ErrForbidden) {
		t.Errorf("ListAccess = %v, want ErrForbidden", err)
	}
	if err := svc.RevokeAccess(ctx, member, team, "someone@wso2.com"); !errors.Is(err, ErrForbidden) {
		t.Errorf("RevokeAccess(other) = %v, want ErrForbidden", err)
	}
}

// A row written straight into the database, without the encryption key, must grant nothing.
func TestForgedGrantIsIgnored(t *testing.T) {
	ctx := context.Background()
	svc, repo, enc := sharingSvc(t)
	grant(t, svc, member, model.RoleRead)
	readSeal := repo.grants[0].Seal

	otherSeal, err := enc.Encrypt(string(model.RoleTransact), accessAAD("0xother", member))
	if err != nil {
		t.Fatal(err)
	}
	forgeries := map[string]GrantRow{
		"made-up seal":                {Address: team, Email: "eve@wso2.com", Level: "TRANSACT", Seal: "bm90LWEtc2VhbA=="},
		"level raised on a real seal": {Address: team, Email: member, Level: "TRANSACT", Seal: readSeal},
		"seal copied to another user": {Address: team, Email: "eve@wso2.com", Level: "READ", Seal: readSeal},
		"seal copied from a wallet":   {Address: team, Email: member, Level: "TRANSACT", Seal: otherSeal},
	}
	// The database matches emails loosely, so a row sealed for one address can be
	// returned for a caller with a different one. The fake repo matches by case only,
	// so stand in for that with a row whose stored email differs from its seal.
	looseSeal, err := enc.Encrypt(string(model.RoleRead), accessAAD(team, "someone-else@wso2.com"))
	if err != nil {
		t.Fatal(err)
	}
	forgeries["row found for a different email"] = GrantRow{Address: team, Email: "eve@wso2.com", Level: "READ", Seal: looseSeal}
	for name, forged := range forgeries {
		t.Run(name, func(t *testing.T) {
			repo.grants = []GrantRow{forged}
			if _, err := svc.Balance(ctx, forged.Email, team); !errors.Is(err, ErrForbidden) {
				t.Errorf("Balance = %v, want ErrForbidden", err)
			}
			wallets, err := svc.ListWallets(ctx, forged.Email)
			if err != nil {
				t.Fatalf("ListWallets: %v", err)
			}
			if len(wallets) != 0 {
				t.Errorf("ListWallets returned %d wallets, want 0", len(wallets))
			}
		})
	}
}

func TestListWalletsIncludesShared(t *testing.T) {
	ctx := context.Background()
	svc, repo, enc := sharingSvc(t)
	seed(t, enc, repo, "0xmine", member, "5", true)
	grant(t, svc, member, model.RoleTransact)

	wallets, err := svc.ListWallets(ctx, member)
	if err != nil {
		t.Fatalf("ListWallets: %v", err)
	}
	if len(wallets) != 2 {
		t.Fatalf("got %d wallets, want 2", len(wallets))
	}
	if wallets[0].Address != "0xmine" || wallets[0].Role != model.RoleOwner || wallets[0].Shared {
		t.Errorf("own wallet = %+v", wallets[0])
	}
	if wallets[1].Address != team || wallets[1].Role != model.RoleTransact || !wallets[1].Shared || wallets[1].DefaultWallet {
		t.Errorf("shared wallet = %+v", wallets[1])
	}
}

func TestWalletNameNeedsSharing(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := sharingSvc(t)

	if err := svc.Rename(ctx, owner, team, "HR Team"); !errors.Is(err, ErrNameNotAllowed) {
		t.Fatalf("Rename before sharing = %v, want ErrNameNotAllowed", err)
	}
	grant(t, svc, member, model.RoleRead)
	if err := svc.Rename(ctx, owner, team, "  HR Team "); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if info, _ := svc.WalletInfo(ctx, team); info.Name != "HR Team" {
		t.Errorf("name while shared = %q, want HR Team", info.Name)
	}

	// The name stays stored but is hidden once nobody else has access.
	if err := svc.RevokeAccess(ctx, owner, team, member); err != nil {
		t.Fatalf("RevokeAccess: %v", err)
	}
	if info, _ := svc.WalletInfo(ctx, team); info.Name != "" {
		t.Errorf("name after last revoke = %q, want empty", info.Name)
	}
	grant(t, svc, member, model.RoleRead)
	if info, _ := svc.WalletInfo(ctx, team); info.Name != "HR Team" {
		t.Errorf("name after re-sharing = %q, want HR Team", info.Name)
	}
}

func TestWalletNameValidationAndTamper(t *testing.T) {
	ctx := context.Background()
	svc, repo, _ := sharingSvc(t)
	grant(t, svc, member, model.RoleRead)

	long := make([]rune, maxNameLength+1)
	for i := range long {
		long[i] = 'a'
	}
	for _, bad := range []string{string(long), "line\nbreak"} {
		if err := svc.Rename(ctx, owner, team, bad); !errors.Is(err, ErrInvalidName) {
			t.Errorf("Rename(%q) = %v, want ErrInvalidName", bad, err)
		}
	}

	repo.find(team).Name = sql.NullString{String: "Rm9yZ2VkIG5hbWU=", Valid: true}
	if info, _ := svc.WalletInfo(ctx, team); info.Name != "" {
		t.Errorf("forged name shown as %q, want empty", info.Name)
	}
}

func TestRevokeAccess(t *testing.T) {
	ctx := context.Background()
	svc, _, _ := sharingSvc(t)
	grant(t, svc, member, model.RoleTransact)

	if err := svc.RevokeAccess(ctx, member, team, member); err != nil {
		t.Fatalf("member removing self: %v", err)
	}
	if _, err := svc.Balance(ctx, member, team); !errors.Is(err, ErrForbidden) {
		t.Errorf("Balance after revoke = %v, want ErrForbidden", err)
	}
	if err := svc.RevokeAccess(ctx, owner, team, member); !errors.Is(err, ErrNotFound) {
		t.Errorf("revoking a missing grant = %v, want ErrNotFound", err)
	}
}
