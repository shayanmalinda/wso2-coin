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

// Package transaction implements master-wallet balances, transfers and history for
// operational clients identified by their client id.
package transaction

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"regexp"
	"strings"
	"time"

	"github.com/wso2/wso2-coin/operations/transaction-service/internal/model"
	"github.com/wso2/wso2-coin/operations/transaction-service/internal/money"
)

// Domain errors returned by the service and mapped to HTTP statuses by the handler.
var (
	ErrNotFound          = errors.New("wallet not found")
	ErrForbidden         = errors.New("caller has no master wallet")
	ErrInvalidAmount     = errors.New("invalid amount")
	ErrInsufficientFunds = errors.New("insufficient funds")
	ErrRecipientNotFound = errors.New("recipient wallet not found")
	ErrSelfTransfer      = errors.New("cannot transfer to the same wallet")
	ErrIntegrity         = errors.New("balance integrity check failed")
	ErrInvalidReference  = errors.New("invalid reference")
	ErrInvalidSource     = errors.New("invalid source")
	ErrReferenceConflict = errors.New("reference already used with different details")
)

// Payment reference and source validation. The reference is a caller-supplied
// idempotency key; the unique index on `reference` guarantees uniqueness, so callers
// may also use the 0x+64hex shape used by service-generated references.
var (
	referencePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,65}$`)
	sourcePattern    = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,31}$`)
)

func validReference(s string) bool {
	return referencePattern.MatchString(s)
}

func validSource(s string) bool {
	return sourcePattern.MatchString(s)
}

// Encryptor authenticates and (de)encrypts stored values, binding them to an AAD.
type Encryptor interface {
	Encrypt(plaintext, aad string) (string, error)
	Decrypt(encoded, aad string) (string, error)
}

// Service holds transaction business logic.
type Service struct {
	repo Repository
	enc  Encryptor
}

// NewService returns a transaction Service.
func NewService(repo Repository, enc Encryptor) *Service {
	return &Service{repo: repo, enc: enc}
}

// MasterBalance returns the decrypted balance of the caller's master wallet.
func (s *Service) MasterBalance(ctx context.Context, clientID string) (model.Balance, error) {
	address, err := s.masterAddress(ctx, clientID)
	if err != nil {
		return model.Balance{}, err
	}
	return s.walletBalance(ctx, address)
}

// TransferResult is the outcome of a master-wallet transfer. Idempotent is true when the
// caller-supplied reference already existed with matching details and the original
// transaction was replayed instead of moving coins again.
type TransferResult struct {
	Response   model.TransferResponse
	Idempotent bool
}

// Transfer moves coins from the caller's master wallet to an existing recipient
// atomically and returns a reference for the recorded transaction. A caller-supplied
// reference is validated and used as the idempotency key; otherwise one is generated.
func (s *Service) Transfer(ctx context.Context, clientID string, req model.TransferRequest) (TransferResult, error) {
	if req.Reference != "" && !validReference(req.Reference) {
		return TransferResult{}, ErrInvalidReference
	}
	if req.Source != "" && !validSource(req.Source) {
		return TransferResult{}, ErrInvalidSource
	}
	from, err := s.masterAddress(ctx, clientID)
	if err != nil {
		return TransferResult{}, err
	}
	amount, err := money.ParseUnits(req.Amount)
	if err != nil || amount.Sign() <= 0 {
		return TransferResult{}, ErrInvalidAmount
	}
	if strings.EqualFold(from, req.ToAddress) {
		return TransferResult{}, ErrSelfTransfer
	}
	to, err := s.repo.WalletByAddress(ctx, req.ToAddress)
	if err != nil {
		return TransferResult{}, err
	}
	if to == nil {
		slog.WarnContext(ctx, "transfer recipient wallet not found", "recipient", req.ToAddress)
		return TransferResult{}, ErrRecipientNotFound
	}

	ref := req.Reference
	if ref == "" {
		if ref, err = newReference(); err != nil {
			return TransferResult{}, err
		}
	}
	err = s.settle(ctx, from, to.Address, amount, ref, req.Source)
	if err == nil {
		return TransferResult{Response: model.TransferResponse{
			Reference:   ref,
			FromAddress: from,
			ToAddress:   to.Address,
			Amount:      money.FormatUnits(amount),
		}}, nil
	}
	// Only a caller-supplied reference can legitimately collide; a generated one
	// colliding is an internal fault and surfaces as such.
	if req.Reference == "" || !errors.Is(err, ErrDuplicateReference) {
		return TransferResult{}, err
	}
	existing, err := s.replay(ctx, ref, from, to.Address, amount)
	if err != nil {
		return TransferResult{}, err
	}
	return TransferResult{
		Response: model.TransferResponse{
			Reference:   ref,
			FromAddress: existing.FromAddress,
			ToAddress:   existing.ToAddress,
			Amount:      money.FormatUnits(amount),
		},
		Idempotent: true,
	}, nil
}

// PaymentResult is the outcome of a payment. Idempotent is true when the reference
// already existed with matching details and the original transaction was replayed.
type PaymentResult struct {
	Response   model.PaymentResponse
	Idempotent bool
}

// Pay collects a payment from the user-owned fromAddress into an existing recipient
// wallet, atomically, keyed by the caller-supplied reference for idempotency. The
// email is the verified owner of fromAddress.
func (s *Service) Pay(ctx context.Context, email string, req model.PaymentRequest) (PaymentResult, error) {
	if !validReference(req.Reference) {
		return PaymentResult{}, ErrInvalidReference
	}
	if !validSource(req.Source) {
		return PaymentResult{}, ErrInvalidSource
	}
	amount, err := money.ParseUnits(req.Amount)
	if err != nil || amount.Sign() <= 0 {
		return PaymentResult{}, ErrInvalidAmount
	}
	if strings.EqualFold(req.FromAddress, req.ToAddress) {
		return PaymentResult{}, ErrSelfTransfer
	}

	owned, err := s.repo.WalletOwnedBy(ctx, req.FromAddress, email)
	if err != nil {
		return PaymentResult{}, err
	}
	if !owned {
		// A not-owned and an unknown wallet are treated identically to avoid
		// enumeration; the email is deliberately omitted from this log line.
		slog.WarnContext(ctx, "denying payment: fromAddress is not owned by the authenticated user")
		return PaymentResult{}, ErrForbidden
	}

	to, err := s.repo.WalletByAddress(ctx, req.ToAddress)
	if err != nil {
		return PaymentResult{}, err
	}
	if to == nil {
		slog.WarnContext(ctx, "payment recipient wallet not found", "recipient", req.ToAddress)
		return PaymentResult{}, ErrRecipientNotFound
	}

	err = s.settle(ctx, req.FromAddress, to.Address, amount, req.Reference, req.Source)
	if err == nil {
		return PaymentResult{Response: model.PaymentResponse{
			Reference:   req.Reference,
			FromAddress: req.FromAddress,
			ToAddress:   to.Address,
			Amount:      money.FormatUnits(amount),
		}}, nil
	}
	if !errors.Is(err, ErrDuplicateReference) {
		return PaymentResult{}, err
	}
	existing, err := s.replay(ctx, req.Reference, req.FromAddress, to.Address, amount)
	if err != nil {
		return PaymentResult{}, err
	}
	return PaymentResult{
		Response: model.PaymentResponse{
			Reference:   req.Reference,
			FromAddress: existing.FromAddress,
			ToAddress:   existing.ToAddress,
			Amount:      money.FormatUnits(amount),
		},
		Idempotent: true,
	}, nil
}

// replay resolves a duplicate-reference insert: it returns the recorded transaction
// when its from/to/amount match the retried request (an idempotent success for the
// caller), or ErrReferenceConflict when they differ.
func (s *Service) replay(ctx context.Context, reference, from, to string, amount *big.Int) (*TxnRow, error) {
	existing, err := s.repo.TransactionByReference(ctx, reference)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, ErrReferenceConflict
	}
	plainAmount, err := s.enc.Decrypt(existing.Amount, amountAADForRow(*existing))
	if err != nil {
		return nil, ErrIntegrity
	}
	existingUnits, err := money.ParseUnits(plainAmount)
	if err != nil {
		return nil, ErrIntegrity
	}
	if !strings.EqualFold(existing.FromAddress, from) ||
		!strings.EqualFold(existing.ToAddress, to) ||
		existingUnits.Cmp(amount) != 0 {
		return nil, ErrReferenceConflict
	}
	return existing, nil
}

// settle performs the atomic locked debit, credit and transaction insert for a
// coin movement of amount from -> to, recorded under reference (and source, when set).
func (s *Service) settle(ctx context.Context, from, to string, amount *big.Int, reference, source string) error {
	encAmount, err := s.enc.Encrypt(money.FormatUnits(amount), amountAADForRef(reference))
	if err != nil {
		return fmt.Errorf("encrypt amount: %w", err)
	}
	return s.repo.Tx(ctx, func(q Queries) error {
		balances, err := s.lockBalances(ctx, q, from, to)
		if err != nil {
			return err
		}
		fromUnits, toUnits := balances[from], balances[to]
		if fromUnits.Cmp(amount) < 0 {
			return ErrInsufficientFunds
		}
		encFrom, err := s.enc.Encrypt(money.FormatUnits(new(big.Int).Sub(fromUnits, amount)), balanceAAD(from))
		if err != nil {
			return fmt.Errorf("encrypt sender balance: %w", err)
		}
		encTo, err := s.enc.Encrypt(money.FormatUnits(new(big.Int).Add(toUnits, amount)), balanceAAD(to))
		if err != nil {
			return fmt.Errorf("encrypt recipient balance: %w", err)
		}
		if err := q.UpdateBalance(ctx, from, encFrom); err != nil {
			return err
		}
		if err := q.UpdateBalance(ctx, to, encTo); err != nil {
			return err
		}
		return q.InsertTransaction(ctx, TxnInsert{
			FromAddress: from,
			ToAddress:   to,
			Amount:      encAmount,
			Reference:   reference,
			Source:      source,
		})
	})
}

// WalletBalance returns the decrypted balance of any wallet by address.
func (s *Service) WalletBalance(ctx context.Context, address string) (model.Balance, error) {
	return s.walletBalance(ctx, address)
}

// ListWallets returns every wallet's address and metadata, newest first.
func (s *Service) ListWallets(ctx context.Context) ([]model.WalletSummary, error) {
	rows, err := s.repo.ListWallets(ctx)
	if err != nil {
		return nil, err
	}
	wallets := make([]model.WalletSummary, 0, len(rows))
	for _, row := range rows {
		wallets = append(wallets, model.WalletSummary{
			WalletAddress: row.Address,
			DefaultWallet: row.DefaultWallet,
			CreatedOn:     row.CreatedOn.UTC().Format(time.RFC3339),
		})
	}
	return wallets, nil
}

// ListUserWallets returns the wallets owned by the given user email with their
// decrypted balances, the default wallet first. It always returns a non-nil slice.
func (s *Service) ListUserWallets(ctx context.Context, email string) ([]model.UserWallet, error) {
	rows, err := s.repo.WalletsByEmail(ctx, email)
	if err != nil {
		return nil, err
	}
	wallets := make([]model.UserWallet, 0, len(rows))
	for _, row := range rows {
		plain, err := s.decryptBalance(&WalletRow{Address: row.Address, Balance: row.Balance})
		if err != nil {
			return nil, err
		}
		wallets = append(wallets, model.UserWallet{
			WalletAddress: row.Address,
			Balance:       plain,
			DefaultWallet: row.DefaultWallet,
		})
	}
	return wallets, nil
}

// ListWalletAddresses returns every distinct wallet address, ordered by address.
func (s *Service) ListWalletAddresses(ctx context.Context) ([]string, error) {
	addresses, err := s.repo.ListWalletAddresses(ctx)
	if err != nil {
		return nil, err
	}
	if addresses == nil {
		return []string{}, nil
	}
	return addresses, nil
}

// SearchTransactions returns a filtered, paginated page of transactions, newest first.
func (s *Service) SearchTransactions(ctx context.Context, req model.TransactionSearchRequest) (model.TransactionPage, error) {
	rows, total, err := s.repo.SearchTransactions(ctx, SearchFilters{
		FromAddresses: req.FromAddresses,
		ToAddresses:   req.ToAddresses,
		Reference:     req.Reference,
		DateFrom:      req.DateFrom,
		DateTo:        req.DateTo,
		Limit:         req.Limit,
		Offset:        req.Offset,
	})
	if err != nil {
		return model.TransactionPage{}, err
	}

	txns := make([]model.Transaction, 0, len(rows))
	for _, row := range rows {
		txn, err := s.toTransaction(row)
		if err != nil {
			return model.TransactionPage{}, err
		}
		txns = append(txns, txn)
	}

	next, previous := pageLinks(req.Limit, req.Offset, total)
	return model.TransactionPage{
		Transactions: txns,
		Total:        total,
		Limit:        req.Limit,
		Offset:       req.Offset,
		Next:         next,
		Previous:     previous,
	}, nil
}

// TransactionByReference returns a single transaction by its reference.
func (s *Service) TransactionByReference(ctx context.Context, reference string) (model.Transaction, error) {
	row, err := s.repo.TransactionByReference(ctx, reference)
	if err != nil {
		return model.Transaction{}, err
	}
	if row == nil {
		return model.Transaction{}, ErrNotFound
	}
	return s.toTransaction(*row)
}

func (s *Service) masterAddress(ctx context.Context, clientID string) (string, error) {
	address, err := s.repo.MasterWalletByClient(ctx, clientID)
	if err != nil {
		if errors.Is(err, ErrNoMasterWallet) {
			// The client id is deliberately omitted from this log line.
			slog.WarnContext(ctx, "denying request: caller has no active master wallet mapping")
			return "", ErrForbidden
		}
		return "", err
	}
	return address, nil
}

func (s *Service) walletBalance(ctx context.Context, address string) (model.Balance, error) {
	w, err := s.repo.WalletByAddress(ctx, address)
	if err != nil {
		return model.Balance{}, err
	}
	if w == nil {
		return model.Balance{}, ErrNotFound
	}
	plain, err := s.decryptBalance(w)
	if err != nil {
		return model.Balance{}, err
	}
	return model.Balance{WalletAddress: w.Address, Balance: plain}, nil
}

func (s *Service) toTransaction(row TxnRow) (model.Transaction, error) {
	amount, err := s.enc.Decrypt(row.Amount, amountAADForRow(row))
	if err != nil {
		return model.Transaction{}, ErrIntegrity
	}
	return model.Transaction{
		Reference:   transactionReference(row),
		FromAddress: row.FromAddress,
		ToAddress:   row.ToAddress,
		Amount:      amount,
		Timestamp:   transactionTime(row).UTC().Format(time.RFC3339),
	}, nil
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

func transactionReference(row TxnRow) string {
	if row.Reference.Valid && row.Reference.String != "" {
		return row.Reference.String
	}
	return row.Hash.String
}

func transactionTime(row TxnRow) time.Time {
	if row.BlockTime.Valid {
		return row.BlockTime.Time
	}
	return row.CreatedOn
}

func pageLinks(limit, offset, total int) (next, previous *string) {
	if limit > 0 && offset+limit < total {
		s := fmt.Sprintf("?offset=%d&limit=%d", offset+limit, limit)
		next = &s
	}
	if offset > 0 {
		prevOffset := offset - limit
		if prevOffset < 0 {
			prevOffset = 0
		}
		s := fmt.Sprintf("?offset=%d&limit=%d", prevOffset, limit)
		previous = &s
	}
	return next, previous
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

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate random bytes: %w", err)
	}
	return "0x" + hex.EncodeToString(b), nil
}
