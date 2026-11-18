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

// Package wallet implements wallet listing, creation, balances, transfers and history.
package wallet

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/wso2/wso2-coin/apps/wso2-wallet/backend/internal/model"
	"github.com/wso2/wso2-coin/apps/wso2-wallet/backend/internal/money"
)

// sourceWalletApp labels transactions originating from the wallet app. Every
// transaction this service writes is a wallet-app transaction, so its provenance
// is set server-side rather than supplied by callers.
const sourceWalletApp = "WALLET_APP"

// Domain errors returned by the service and mapped to HTTP statuses by the handler.
var (
	ErrNotFound          = errors.New("wallet not found")
	ErrForbidden         = errors.New("wallet not owned by caller")
	ErrInvalidAmount     = errors.New("invalid amount")
	ErrInsufficientFunds = errors.New("insufficient funds")
	ErrRecipientNotFound = errors.New("recipient wallet not found")
	ErrSelfTransfer      = errors.New("cannot transfer to the same wallet")
	ErrIntegrity         = errors.New("balance integrity check failed")
	ErrInvalidName       = errors.New("invalid wallet name")
	ErrNameNotAllowed    = errors.New("wallet must be shared before it can be named")
	ErrInvalidGrant      = errors.New("invalid access grant")
	ErrDomainNotAllowed  = errors.New("email domain not allowed for wallet sharing")
)

const (
	// maxNameLength is the longest wallet name accepted, in characters.
	maxNameLength = 50
	// maxEmailLength is the longest email accepted for a grant; it fits the column.
	maxEmailLength = 254
)

// Encryptor authenticates and (de)encrypts stored values, binding them to an AAD.
type Encryptor interface {
	Encrypt(plaintext, aad string) (string, error)
	Decrypt(encoded, aad string) (string, error)
}

// InitialCoins configures the one-time allocation on a user's first wallet.
type InitialCoins struct {
	Enabled       bool
	Amount        string
	FundingWallet string
	EmailDomain   string
}

// Sharing configures who may take part in wallet sharing.
type Sharing struct {
	// EmailDomain is the domain both the owner and the grantee must belong to.
	EmailDomain string
}

// Service holds wallet business logic.
type Service struct {
	repo         Repository
	enc          Encryptor
	initialCoins InitialCoins
	sharing      Sharing
}

// NewService returns a wallet Service.
func NewService(repo Repository, enc Encryptor, initialCoins InitialCoins, sharing Sharing) *Service {
	return &Service{repo: repo, enc: enc, initialCoins: initialCoins, sharing: sharing}
}

// ListWallets returns the wallets the caller owns, followed by the wallets shared
// with them.
func (s *Service) ListWallets(ctx context.Context, email string) ([]model.Wallet, error) {
	owned, err := s.repo.WalletsByEmail(ctx, email)
	if err != nil {
		return nil, err
	}
	shared, err := s.repo.SharedWalletsByEmail(ctx, normEmail(email))
	if err != nil {
		return nil, err
	}
	wallets := make([]model.Wallet, 0, len(owned)+len(shared))
	for i := range owned {
		w := &owned[i]
		wallets = append(wallets, model.Wallet{
			Address:       w.Address,
			DefaultWallet: w.DefaultWallet,
			CreatedOn:     w.CreatedOn.UTC().Format(time.RFC3339),
			Name:          s.displayName(ctx, w),
			Role:          model.RoleOwner,
			Shared:        w.GrantCount > 0,
		})
	}
	for i := range shared {
		w := &shared[i].Wallet
		role, ok := s.grantRole(shared[i].Grant, email)
		if !ok {
			slog.WarnContext(ctx, "ignoring wallet grant that failed its integrity check", "wallet", w.Address)
			continue
		}
		wallets = append(wallets, model.Wallet{
			Address:   w.Address,
			CreatedOn: w.CreatedOn.UTC().Format(time.RFC3339),
			Name:      s.displayName(ctx, w),
			Role:      role,
			Shared:    true,
		})
	}
	return wallets, nil
}

// CreateWallet provisions a new wallet for the caller, allocating initial coins when
// it is their first wallet and the policy applies.
func (s *Service) CreateWallet(ctx context.Context, email string) (model.Wallet, error) {
	address, err := newAddress()
	if err != nil {
		return model.Wallet{}, err
	}

	var isDefault bool
	err = s.repo.Tx(ctx, func(q Queries) error {
		// Serialize concurrent creates for this user so exactly one wallet becomes the
		// default and initial coins are granted at most once. When initial coins are
		// enabled every create contends on the shared funding-wallet lock, and the
		// user's wallet count is read under that lock — making first-wallet selection
		// race-free across both the allocation and no-allocation paths.
		var fundingUnits *big.Int
		if s.initialCoins.Enabled {
			fundingUnits, err = s.lockedUnits(ctx, q, s.initialCoins.FundingWallet)
			if err != nil {
				return err
			}
		}
		count, err := q.CountWalletsByEmail(ctx, email)
		if err != nil {
			return err
		}
		isDefault = count == 0

		if isDefault && s.allocationApplies(email) {
			return s.allocateFirstWallet(ctx, q, address, email, fundingUnits)
		}
		encZero, err := s.enc.Encrypt(money.FormatUnits(big.NewInt(0)), balanceAAD(address))
		if err != nil {
			return err
		}
		return q.InsertWallet(ctx, address, email, encZero, isDefault)
	})
	if err != nil {
		return model.Wallet{}, err
	}

	return model.Wallet{
		Address: address, DefaultWallet: isDefault, CreatedOn: time.Now().UTC().Format(time.RFC3339), Role: model.RoleOwner,
	}, nil
}

func (s *Service) allocationApplies(email string) bool {
	return s.initialCoins.Enabled &&
		strings.HasSuffix(strings.ToLower(strings.TrimSpace(email)), "@"+strings.ToLower(s.initialCoins.EmailDomain))
}

// allocateFirstWallet inserts the caller's first wallet with the initial coin
// allocation and debits the funding wallet, which the caller has already locked and
// read as fundingUnits.
func (s *Service) allocateFirstWallet(ctx context.Context, q Queries, address, email string, fundingUnits *big.Int) error {
	amount, err := money.ParseUnits(s.initialCoins.Amount)
	if err != nil {
		return fmt.Errorf("parse initial coins amount: %w", err)
	}
	if amount.Sign() <= 0 {
		return fmt.Errorf("initial coins amount must be positive, got %q", s.initialCoins.Amount)
	}
	if fundingUnits.Cmp(amount) < 0 {
		return ErrInsufficientFunds
	}
	ref, err := newReference()
	if err != nil {
		return err
	}
	funding := s.initialCoins.FundingWallet
	encInitial, err := s.enc.Encrypt(money.FormatUnits(amount), balanceAAD(address))
	if err != nil {
		return err
	}
	encAmount, err := s.enc.Encrypt(money.FormatUnits(amount), amountAADForRef(ref))
	if err != nil {
		return err
	}
	encFunding, err := s.enc.Encrypt(money.FormatUnits(new(big.Int).Sub(fundingUnits, amount)), balanceAAD(funding))
	if err != nil {
		return err
	}
	if err := q.InsertWallet(ctx, address, email, encInitial, true); err != nil {
		return err
	}
	if err := q.UpdateBalance(ctx, funding, encFunding); err != nil {
		return err
	}
	return q.InsertTransaction(ctx, TxnInsert{FromAddress: funding, ToAddress: address, Amount: encAmount, Reference: ref, Source: sourceWalletApp})
}

// SetPrimary marks one of the caller's wallets as their default.
func (s *Service) SetPrimary(ctx context.Context, email, address string) error {
	w, err := s.ownedWallet(ctx, email, address)
	if err != nil {
		return err
	}
	return s.repo.SetPrimary(ctx, email, w.Address)
}

// Balance returns the decrypted balance of a wallet the caller can access.
func (s *Service) Balance(ctx context.Context, email, address string) (model.Balance, error) {
	w, _, err := s.access(ctx, email, address)
	if err != nil {
		return model.Balance{}, err
	}
	plain, err := s.decryptBalance(w)
	if err != nil {
		return model.Balance{}, err
	}
	return model.Balance{Address: w.Address, Balance: plain}, nil
}

// Transactions returns a page of history, newest first, for a wallet the caller can access.
func (s *Service) Transactions(ctx context.Context, email, address string, limit, offset int, direction string) (model.TransactionPage, error) {
	w, _, err := s.access(ctx, email, address)
	if err != nil {
		return model.TransactionPage{}, err
	}
	rows, err := s.repo.Transactions(ctx, w.Address, limit+1, offset, direction)
	if err != nil {
		return model.TransactionPage{}, err
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}

	txns := make([]model.Transaction, 0, len(rows))
	for _, row := range rows {
		amount, err := s.enc.Decrypt(row.Amount, amountAADForRow(row))
		if err != nil {
			return model.TransactionPage{}, ErrIntegrity
		}
		dir := directionFor(w.Address, row.FromAddress)
		txns = append(txns, model.Transaction{
			TransactionHash: transactionHash(row),
			FromAddress:     row.FromAddress,
			ToAddress:       row.ToAddress,
			Amount:          amount,
			Direction:       dir,
			Timestamp:       transactionTime(row).UTC().Format(time.RFC3339),
			InitiatedBy:     s.initiator(ctx, row, dir),
		})
	}
	return model.TransactionPage{Transactions: txns, Limit: limit, Offset: offset, HasMore: hasMore}, nil
}

// Transfer moves coins between two wallets atomically and returns the transaction hash.
func (s *Service) Transfer(ctx context.Context, email string, req model.TransferRequest) (model.TransferResponse, error) {
	amount, err := money.ParseUnits(req.Amount)
	if err != nil || amount.Sign() <= 0 {
		return model.TransferResponse{}, ErrInvalidAmount
	}
	if strings.EqualFold(req.FromAddress, req.ToAddress) {
		return model.TransferResponse{}, ErrSelfTransfer
	}
	from, role, err := s.access(ctx, email, req.FromAddress)
	if err != nil {
		return model.TransferResponse{}, err
	}
	if role == model.RoleRead {
		return model.TransferResponse{}, ErrForbidden
	}
	to, err := s.repo.WalletByAddress(ctx, req.ToAddress)
	if err != nil {
		return model.TransferResponse{}, err
	}
	if to == nil {
		return model.TransferResponse{}, ErrRecipientNotFound
	}

	ref, err := newReference()
	if err != nil {
		return model.TransferResponse{}, err
	}
	encAmount, err := s.enc.Encrypt(money.FormatUnits(amount), amountAADForRef(ref))
	if err != nil {
		return model.TransferResponse{}, err
	}
	encInitiator, err := s.enc.Encrypt(normEmail(email), initiatorAAD(ref))
	if err != nil {
		return model.TransferResponse{}, err
	}

	err = s.repo.Tx(ctx, func(q Queries) error {
		// Re-check the grant under a lock so a revoke or downgrade cannot slip in
		// between the access check above and the debit.
		if role != model.RoleOwner {
			g, err := q.LockGrant(ctx, from.Address, normEmail(email))
			if err != nil {
				return err
			}
			if g == nil {
				return ErrForbidden
			}
			if locked, ok := s.grantRole(*g, email); !ok || locked != model.RoleTransact {
				return ErrForbidden
			}
		}
		balances, err := s.lockBalances(ctx, q, from.Address, to.Address)
		if err != nil {
			return err
		}
		fromUnits, toUnits := balances[from.Address], balances[to.Address]
		if fromUnits.Cmp(amount) < 0 {
			return ErrInsufficientFunds
		}
		encFrom, err := s.enc.Encrypt(money.FormatUnits(new(big.Int).Sub(fromUnits, amount)), balanceAAD(from.Address))
		if err != nil {
			return err
		}
		encTo, err := s.enc.Encrypt(money.FormatUnits(new(big.Int).Add(toUnits, amount)), balanceAAD(to.Address))
		if err != nil {
			return err
		}
		if err := q.UpdateBalance(ctx, from.Address, encFrom); err != nil {
			return err
		}
		if err := q.UpdateBalance(ctx, to.Address, encTo); err != nil {
			return err
		}
		return q.InsertTransaction(ctx, TxnInsert{
			FromAddress: from.Address, ToAddress: to.Address, Amount: encAmount, Reference: ref,
			Source: sourceWalletApp, InitiatedBy: encInitiator,
		})
	})
	if err != nil {
		return model.TransferResponse{}, err
	}
	return model.TransferResponse{TransactionHash: ref}, nil
}

// WalletHealth reports whether the wallet exists and its balance decrypts cleanly.
func (s *Service) WalletHealth(ctx context.Context, email, address string) (model.WalletHealth, error) {
	w, err := s.repo.WalletByAddress(ctx, address)
	if err != nil {
		return model.WalletHealth{}, err
	}
	if w == nil {
		return model.WalletHealth{Healthy: false, Exists: false, BalanceIntact: false}, nil
	}
	if _, _, err := s.access(ctx, email, address); err != nil {
		return model.WalletHealth{}, err
	}
	_, decErr := s.decryptBalance(w)
	intact := decErr == nil
	return model.WalletHealth{Healthy: intact, Exists: true, BalanceIntact: intact}, nil
}

// Rename sets the display name of a wallet the caller owns. A wallet can only be
// named while it is shared, so personal wallets cannot carry a made-up name. An
// empty name clears it.
func (s *Service) Rename(ctx context.Context, email, address, name string) error {
	w, err := s.ownedWallet(ctx, email, address)
	if err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return s.repo.SetWalletName(ctx, w.Address, sql.NullString{})
	}
	if !validName(name) {
		return ErrInvalidName
	}
	enc, err := s.enc.Encrypt(name, nameAAD(w.Address))
	if err != nil {
		return err
	}
	// The shared check and the write are one statement, so a grant removed in
	// between cannot leave a name on a wallet that is no longer shared.
	named, err := s.repo.SetWalletNameIfShared(ctx, w.Address, enc)
	if err != nil {
		return err
	}
	if !named {
		return ErrNameNotAllowed
	}
	return nil
}

// WalletInfo returns what anyone may know about a wallet before sending to it: that
// it exists and, while it is shared, its name.
func (s *Service) WalletInfo(ctx context.Context, address string) (model.WalletInfo, error) {
	w, err := s.repo.WalletByAddress(ctx, address)
	if err != nil {
		return model.WalletInfo{}, err
	}
	if w == nil {
		return model.WalletInfo{}, ErrNotFound
	}
	return model.WalletInfo{Address: w.Address, Name: s.displayName(ctx, w)}, nil
}

// ListAccess returns the people the owner has shared the wallet with.
func (s *Service) ListAccess(ctx context.Context, email, address string) ([]model.AccessGrant, error) {
	w, err := s.ownedWallet(ctx, email, address)
	if err != nil {
		return nil, err
	}
	rows, err := s.repo.Grants(ctx, w.Address)
	if err != nil {
		return nil, err
	}
	grants := make([]model.AccessGrant, 0, len(rows))
	for _, g := range rows {
		role, ok := s.grantRole(g, g.Email)
		if !ok {
			slog.WarnContext(ctx, "ignoring wallet grant that failed its integrity check", "wallet", w.Address)
			continue
		}
		grants = append(grants, model.AccessGrant{
			Email:       g.Email,
			AccessLevel: role,
			GrantedBy:   g.GrantedBy,
			GrantedOn:   g.CreatedOn.UTC().Format(time.RFC3339),
		})
	}
	return grants, nil
}

// GrantAccess gives a person access to a wallet the caller owns, or changes the
// level of an existing grant. Both people must belong to the sharing domain.
func (s *Service) GrantAccess(ctx context.Context, email, address string, req model.AccessRequest) error {
	w, err := s.ownedWallet(ctx, email, address)
	if err != nil {
		return err
	}
	grantee, ok := parseEmail(req.Email)
	if !ok || ownedBy(w, grantee) {
		return ErrInvalidGrant
	}
	if req.AccessLevel != model.RoleRead && req.AccessLevel != model.RoleTransact {
		return ErrInvalidGrant
	}
	if !s.inSharingDomain(email) || !s.inSharingDomain(grantee) {
		return ErrDomainNotAllowed
	}
	seal, err := s.enc.Encrypt(string(req.AccessLevel), accessAAD(w.Address, grantee))
	if err != nil {
		return err
	}
	return s.repo.UpsertGrant(ctx, GrantRow{
		Address: w.Address, Email: grantee, Level: string(req.AccessLevel), Seal: seal, GrantedBy: normEmail(email),
	})
}

// RevokeAccess lets the wallet owner remove a person's access.
func (s *Service) RevokeAccess(ctx context.Context, email, address, grantee string) error {
	w, err := s.ownedWallet(ctx, email, address)
	if err != nil {
		return err
	}
	g, ok := parseEmail(grantee)
	if !ok {
		return ErrNotFound
	}
	return s.deleteGrant(ctx, w.Address, g)
}

// LeaveWallet removes the caller's own access to a wallet shared with them.
func (s *Service) LeaveWallet(ctx context.Context, email, address string) error {
	w, err := s.repo.WalletByAddress(ctx, address)
	if err != nil {
		return err
	}
	if w == nil {
		return ErrNotFound
	}
	// The database matches emails loosely, so an identity that is not plain ASCII
	// must not be able to reach another person's grant.
	self, ok := parseEmail(email)
	if !ok {
		return ErrNotFound
	}
	return s.deleteGrant(ctx, w.Address, self)
}

func (s *Service) deleteGrant(ctx context.Context, address, grantee string) error {
	removed, err := s.repo.DeleteGrant(ctx, address, grantee)
	if err != nil {
		return err
	}
	if !removed {
		return ErrNotFound
	}
	return nil
}

// access resolves what the caller may do with a wallet: the owner gets RoleOwner,
// a person with a valid grant gets that grant's role, and anyone else is refused.
func (s *Service) access(ctx context.Context, email, address string) (*WalletRow, model.Role, error) {
	w, err := s.repo.WalletByAddress(ctx, address)
	if err != nil {
		return nil, "", err
	}
	if w == nil {
		return nil, "", ErrNotFound
	}
	if ownedBy(w, email) {
		return w, model.RoleOwner, nil
	}
	g, err := s.repo.Grant(ctx, w.Address, normEmail(email))
	if err != nil {
		return nil, "", err
	}
	if g == nil {
		return nil, "", ErrForbidden
	}
	role, ok := s.grantRole(*g, email)
	if !ok {
		slog.WarnContext(ctx, "denying access: wallet grant failed its integrity check", "wallet", w.Address)
		return nil, "", ErrForbidden
	}
	return w, role, nil
}

// grantRole verifies a grant's seal for the given person and returns its role. A row
// written or edited without the encryption key fails here and grants nothing.
//
// The seal is checked against email, the person the grant is being used by, rather
// than the email stored on the row: the database matches emails loosely (case and,
// on some collations, accents), so a row found for the caller may have been sealed
// for a different address.
func (s *Service) grantRole(g GrantRow, email string) (model.Role, bool) {
	sealed, err := s.enc.Decrypt(g.Seal, accessAAD(g.Address, email))
	if err != nil || sealed != g.Level {
		return "", false
	}
	switch role := model.Role(sealed); role {
	case model.RoleRead, model.RoleTransact:
		return role, true
	}
	return "", false
}

// displayName returns the wallet's name, or "" when it has none or is not shared.
func (s *Service) displayName(ctx context.Context, w *WalletRow) string {
	if w.GrantCount == 0 || !w.Name.Valid || w.Name.String == "" {
		return ""
	}
	name, err := s.enc.Decrypt(w.Name.String, nameAAD(w.Address))
	if err != nil {
		slog.WarnContext(ctx, "ignoring wallet name that failed its integrity check", "wallet", w.Address)
		return ""
	}
	return name
}

// initiator returns who made a transfer, for transfers sent from the viewed wallet.
// The recipient's side never sees it.
func (s *Service) initiator(ctx context.Context, row TxnRow, dir model.Direction) string {
	if dir != model.DirectionSent || !row.InitiatedBy.Valid || row.InitiatedBy.String == "" || !row.Reference.Valid {
		return ""
	}
	email, err := s.enc.Decrypt(row.InitiatedBy.String, initiatorAAD(row.Reference.String))
	if err != nil {
		slog.WarnContext(ctx, "ignoring transaction initiator that failed its integrity check", "reference", row.Reference.String)
		return ""
	}
	return email
}

func (s *Service) inSharingDomain(email string) bool {
	domain := strings.ToLower(strings.TrimSpace(s.sharing.EmailDomain))
	return domain != "" && strings.HasSuffix(normEmail(email), "@"+domain)
}

func (s *Service) ownedWallet(ctx context.Context, email, address string) (*WalletRow, error) {
	w, err := s.repo.WalletByAddress(ctx, address)
	if err != nil {
		return nil, err
	}
	if w == nil {
		return nil, ErrNotFound
	}
	if !ownedBy(w, email) {
		return nil, ErrForbidden
	}
	return w, nil
}

func (s *Service) decryptBalance(w *WalletRow) (string, error) {
	if !w.Balance.Valid {
		return "", ErrIntegrity
	}
	plain, err := s.enc.Decrypt(w.Balance.String, balanceAAD(w.Address))
	if err != nil {
		return "", ErrIntegrity
	}
	return plain, nil
}

func (s *Service) lockedUnits(ctx context.Context, q Queries, address string) (*big.Int, error) {
	enc, err := q.LockBalance(ctx, address)
	if err != nil {
		return nil, err
	}
	plain, err := s.enc.Decrypt(enc, balanceAAD(address))
	if err != nil {
		return nil, ErrIntegrity
	}
	return money.ParseUnits(plain)
}

// lockBalances locks both wallets in a deterministic order (to avoid deadlocks
// between concurrent transfers) and returns their decrypted unit balances keyed by
// address.
func (s *Service) lockBalances(ctx context.Context, q Queries, a, b string) (map[string]*big.Int, error) {
	first, second := a, b
	if strings.ToLower(second) < strings.ToLower(first) {
		first, second = second, first
	}
	balances := make(map[string]*big.Int, 2)
	for _, addr := range []string{first, second} {
		units, err := s.lockedUnits(ctx, q, addr)
		if err != nil {
			return nil, err
		}
		balances[addr] = units
	}
	return balances, nil
}

func ownedBy(w *WalletRow, email string) bool {
	return strings.EqualFold(strings.TrimSpace(w.Email), strings.TrimSpace(email))
}

func directionFor(wallet, from string) model.Direction {
	if strings.EqualFold(wallet, from) {
		return model.DirectionSent
	}
	return model.DirectionReceived
}

func transactionHash(row TxnRow) string {
	if row.Hash.Valid && row.Hash.String != "" {
		return row.Hash.String
	}
	return row.Reference.String
}

func transactionTime(row TxnRow) time.Time {
	if row.BlockTime.Valid {
		return row.BlockTime.Time
	}
	return row.CreatedOn
}

func normEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// parseEmail trims and validates an address before lowercasing it, so characters
// that lowercase to ASCII (such as the Kelvin sign) are rejected, not folded.
func parseEmail(email string) (string, bool) {
	email = strings.TrimSpace(email)
	if !validEmail(email) {
		return "", false
	}
	return strings.ToLower(email), true
}

// validEmail is a shape check only; the sharing-domain check does the real gating.
func validEmail(email string) bool {
	if len(email) > maxEmailLength {
		return false
	}
	for _, r := range email {
		// Printable ASCII only: no spaces, control characters or look-alike letters.
		if r <= ' ' || r > '~' {
			return false
		}
	}
	local, domain, ok := strings.Cut(email, "@")
	return ok && local != "" && domain != "" && !strings.Contains(domain, "@")
}

func validName(name string) bool {
	if utf8.RuneCountInString(name) > maxNameLength {
		return false
	}
	for _, r := range name {
		if !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}

func accessAAD(address, email string) string {
	return "o2c:access:" + strings.ToLower(address) + ":" + normEmail(email)
}

func nameAAD(address string) string {
	return "o2c:name:" + strings.ToLower(address)
}

func initiatorAAD(reference string) string {
	return "o2c:initiator:ref:" + reference
}

func balanceAAD(address string) string {
	return "o2c:balance:" + strings.ToLower(address)
}

func amountAADForRef(reference string) string {
	return "o2c:amount:ref:" + reference
}

func amountAADForRow(row TxnRow) string {
	if row.Hash.Valid && row.Hash.String != "" {
		return fmt.Sprintf("o2c:amount:%s:%d", strings.ToLower(row.Hash.String), row.LogIndex.Int64)
	}
	return amountAADForRef(row.Reference.String)
}

func newReference() (string, error) {
	return randomHex(32)
}

func newAddress() (string, error) {
	return randomHex(20)
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate random bytes: %w", err)
	}
	return "0x" + hex.EncodeToString(b), nil
}
