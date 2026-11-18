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
	"fmt"
	"time"
)

// WalletRow is a persisted wallet.
type WalletRow struct {
	Address       string
	Email         string
	DefaultWallet bool
	CreatedOn     time.Time
	Balance       sql.NullString
	// Name is the encrypted display name, when one has been set.
	Name sql.NullString
	// GrantCount is the number of people other than the owner with access.
	GrantCount int
}

// GrantRow is one person's access to a wallet they do not own.
type GrantRow struct {
	Address   string
	Email     string
	Level     string
	Seal      string
	GrantedBy string
	CreatedOn time.Time
}

// SharedWalletRow is a wallet the caller can use through a grant.
type SharedWalletRow struct {
	Wallet WalletRow
	Grant  GrantRow
}

// TxnRow is a persisted transaction as read from storage.
type TxnRow struct {
	Hash        sql.NullString
	Reference   sql.NullString
	FromAddress string
	ToAddress   string
	Amount      string
	LogIndex    sql.NullInt64
	BlockTime   sql.NullTime
	CreatedOn   time.Time
	// InitiatedBy is the encrypted email of the user who made the transfer.
	InitiatedBy sql.NullString
}

// TxnInsert is a new transaction to persist.
type TxnInsert struct {
	FromAddress string
	ToAddress   string
	Amount      string
	Reference   string
	// Source labels the originating channel/app for the transaction.
	Source string
	// InitiatedBy is the encrypted email of the user who made the transfer.
	InitiatedBy string
}

// Queries is the set of reads and writes available inside a database transaction.
type Queries interface {
	LockBalance(ctx context.Context, address string) (string, error)
	CountWalletsByEmail(ctx context.Context, email string) (int, error)
	UpdateBalance(ctx context.Context, address, encBalance string) error
	InsertWallet(ctx context.Context, address, email, encBalance string, isDefault bool) error
	InsertTransaction(ctx context.Context, t TxnInsert) error
}

// Repository is the wallet persistence contract.
type Repository interface {
	WalletsByEmail(ctx context.Context, email string) ([]WalletRow, error)
	WalletByAddress(ctx context.Context, address string) (*WalletRow, error)
	SetPrimary(ctx context.Context, email, address string) error
	Transactions(ctx context.Context, address string, limit, offset int, direction string) ([]TxnRow, error)
	SetWalletName(ctx context.Context, address string, encName sql.NullString) error
	SharedWalletsByEmail(ctx context.Context, email string) ([]SharedWalletRow, error)
	Grant(ctx context.Context, address, email string) (*GrantRow, error)
	Grants(ctx context.Context, address string) ([]GrantRow, error)
	UpsertGrant(ctx context.Context, g GrantRow) error
	DeleteGrant(ctx context.Context, address, email string) (bool, error)
	Tx(ctx context.Context, fn func(Queries) error) error
}

type mysqlRepository struct {
	db *sql.DB
}

// NewRepository returns a MySQL-backed Repository.
func NewRepository(db *sql.DB) Repository {
	return &mysqlRepository{db: db}
}

func (r *mysqlRepository) WalletsByEmail(ctx context.Context, email string) ([]WalletRow, error) {
	const q = `SELECT w.wallet_address, w.default_wallet, w.created_on, w.wallet_name,
			(SELECT COUNT(*) FROM wallet_access a WHERE a.wallet_address = w.wallet_address)
		FROM user_wallet w WHERE w.user_email = ?
		ORDER BY w.default_wallet DESC, w.created_on DESC`
	rows, err := r.db.QueryContext(ctx, q, email)
	if err != nil {
		return nil, fmt.Errorf("query wallets: %w", err)
	}
	defer rows.Close()

	var wallets []WalletRow
	for rows.Next() {
		var wr WalletRow
		if err := rows.Scan(&wr.Address, &wr.DefaultWallet, &wr.CreatedOn, &wr.Name, &wr.GrantCount); err != nil {
			return nil, fmt.Errorf("scan wallet: %w", err)
		}
		wr.Email = email
		wallets = append(wallets, wr)
	}
	return wallets, rows.Err()
}

func (r *mysqlRepository) WalletByAddress(ctx context.Context, address string) (*WalletRow, error) {
	const q = `SELECT w.wallet_address, w.user_email, w.default_wallet, w.created_on, w.total_balance, w.wallet_name,
			(SELECT COUNT(*) FROM wallet_access a WHERE a.wallet_address = w.wallet_address)
		FROM user_wallet w WHERE w.wallet_address = ?`
	var wr WalletRow
	err := r.db.QueryRowContext(ctx, q, address).
		Scan(&wr.Address, &wr.Email, &wr.DefaultWallet, &wr.CreatedOn, &wr.Balance, &wr.Name, &wr.GrantCount)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query wallet: %w", err)
	}
	return &wr, nil
}

func (r *mysqlRepository) SetPrimary(ctx context.Context, email, address string) error {
	const q = `UPDATE user_wallet SET default_wallet = (wallet_address = ?) WHERE user_email = ?`
	if _, err := r.db.ExecContext(ctx, q, address, email); err != nil {
		return fmt.Errorf("set primary wallet: %w", err)
	}
	return nil
}

func (r *mysqlRepository) Transactions(ctx context.Context, address string, limit, offset int, direction string) ([]TxnRow, error) {
	where := "from_address = ? OR to_address = ?"
	args := []any{address, address}
	switch direction {
	case "sent":
		where, args = "from_address = ?", []any{address}
	case "received":
		where, args = "to_address = ?", []any{address}
	}
	q := fmt.Sprintf(`SELECT tx_hash, reference, from_address, to_address, amount, tx_log_index, tx_block_timestamp, created_on, initiated_by
		FROM `+"`transaction`"+` WHERE %s
		ORDER BY COALESCE(tx_block_timestamp, created_on) DESC, id DESC
		LIMIT ? OFFSET ?`, where)
	args = append(args, limit, offset)

	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query transactions: %w", err)
	}
	defer rows.Close()

	var txns []TxnRow
	for rows.Next() {
		var t TxnRow
		if err := rows.Scan(&t.Hash, &t.Reference, &t.FromAddress, &t.ToAddress, &t.Amount, &t.LogIndex, &t.BlockTime, &t.CreatedOn, &t.InitiatedBy); err != nil {
			return nil, fmt.Errorf("scan transaction: %w", err)
		}
		txns = append(txns, t)
	}
	return txns, rows.Err()
}

func (r *mysqlRepository) SetWalletName(ctx context.Context, address string, encName sql.NullString) error {
	const q = `UPDATE user_wallet SET wallet_name = ? WHERE wallet_address = ?`
	if _, err := r.db.ExecContext(ctx, q, encName, address); err != nil {
		return fmt.Errorf("set wallet name: %w", err)
	}
	return nil
}

func (r *mysqlRepository) SharedWalletsByEmail(ctx context.Context, email string) ([]SharedWalletRow, error) {
	const q = `SELECT w.wallet_address, w.user_email, w.created_on, w.wallet_name,
			a.user_email, a.access_level, a.seal, a.granted_by, a.created_on
		FROM wallet_access a JOIN user_wallet w ON w.wallet_address = a.wallet_address
		WHERE a.user_email = ?
		ORDER BY a.created_on DESC`
	rows, err := r.db.QueryContext(ctx, q, email)
	if err != nil {
		return nil, fmt.Errorf("query shared wallets: %w", err)
	}
	defer rows.Close()

	var shared []SharedWalletRow
	for rows.Next() {
		var sr SharedWalletRow
		if err := rows.Scan(&sr.Wallet.Address, &sr.Wallet.Email, &sr.Wallet.CreatedOn, &sr.Wallet.Name,
			&sr.Grant.Email, &sr.Grant.Level, &sr.Grant.Seal, &sr.Grant.GrantedBy, &sr.Grant.CreatedOn); err != nil {
			return nil, fmt.Errorf("scan shared wallet: %w", err)
		}
		sr.Grant.Address = sr.Wallet.Address
		// A row here is itself a grant, so the wallet is shared with at least one person.
		sr.Wallet.GrantCount = 1
		shared = append(shared, sr)
	}
	return shared, rows.Err()
}

func (r *mysqlRepository) Grant(ctx context.Context, address, email string) (*GrantRow, error) {
	const q = `SELECT wallet_address, user_email, access_level, seal, granted_by, created_on
		FROM wallet_access WHERE wallet_address = ? AND user_email = ?`
	var g GrantRow
	err := r.db.QueryRowContext(ctx, q, address, email).
		Scan(&g.Address, &g.Email, &g.Level, &g.Seal, &g.GrantedBy, &g.CreatedOn)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query grant: %w", err)
	}
	return &g, nil
}

func (r *mysqlRepository) Grants(ctx context.Context, address string) ([]GrantRow, error) {
	const q = `SELECT wallet_address, user_email, access_level, seal, granted_by, created_on
		FROM wallet_access WHERE wallet_address = ? ORDER BY user_email`
	rows, err := r.db.QueryContext(ctx, q, address)
	if err != nil {
		return nil, fmt.Errorf("query grants: %w", err)
	}
	defer rows.Close()

	var grants []GrantRow
	for rows.Next() {
		var g GrantRow
		if err := rows.Scan(&g.Address, &g.Email, &g.Level, &g.Seal, &g.GrantedBy, &g.CreatedOn); err != nil {
			return nil, fmt.Errorf("scan grant: %w", err)
		}
		grants = append(grants, g)
	}
	return grants, rows.Err()
}

func (r *mysqlRepository) UpsertGrant(ctx context.Context, g GrantRow) error {
	const q = `INSERT INTO wallet_access (wallet_address, user_email, access_level, seal, granted_by)
		VALUES (?, ?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE access_level = VALUES(access_level), seal = VALUES(seal), granted_by = VALUES(granted_by)`
	if _, err := r.db.ExecContext(ctx, q, g.Address, g.Email, g.Level, g.Seal, g.GrantedBy); err != nil {
		return fmt.Errorf("upsert grant: %w", err)
	}
	return nil
}

func (r *mysqlRepository) DeleteGrant(ctx context.Context, address, email string) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		"DELETE FROM wallet_access WHERE wallet_address = ? AND user_email = ?", address, email)
	if err != nil {
		return false, fmt.Errorf("delete grant: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("delete grant: %w", err)
	}
	return n > 0, nil
}

func (r *mysqlRepository) Tx(ctx context.Context, fn func(Queries) error) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if err := fn(&txQueries{tx: tx}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

type txQueries struct {
	tx *sql.Tx
}

func (q *txQueries) LockBalance(ctx context.Context, address string) (string, error) {
	var bal sql.NullString
	err := q.tx.QueryRowContext(ctx,
		"SELECT total_balance FROM user_wallet WHERE wallet_address = ? FOR UPDATE", address).Scan(&bal)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("wallet %s not found", address)
	}
	if err != nil {
		return "", fmt.Errorf("lock balance: %w", err)
	}
	if !bal.Valid {
		return "", fmt.Errorf("wallet %s has no balance", address)
	}
	return bal.String, nil
}

func (q *txQueries) CountWalletsByEmail(ctx context.Context, email string) (int, error) {
	var n int
	err := q.tx.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM user_wallet WHERE user_email = ?", email).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count wallets: %w", err)
	}
	return n, nil
}

func (q *txQueries) UpdateBalance(ctx context.Context, address, encBalance string) error {
	if _, err := q.tx.ExecContext(ctx,
		"UPDATE user_wallet SET total_balance = ? WHERE wallet_address = ?", encBalance, address); err != nil {
		return fmt.Errorf("update balance: %w", err)
	}
	return nil
}

func (q *txQueries) InsertWallet(ctx context.Context, address, email, encBalance string, isDefault bool) error {
	if _, err := q.tx.ExecContext(ctx,
		"INSERT INTO user_wallet (wallet_address, user_email, default_wallet, total_balance) VALUES (?, ?, ?, ?)",
		address, email, isDefault, encBalance); err != nil {
		return fmt.Errorf("insert wallet: %w", err)
	}
	return nil
}

func (q *txQueries) InsertTransaction(ctx context.Context, t TxnInsert) error {
	const stmt = "INSERT INTO `transaction` (from_address, to_address, amount, reference, source, initiated_by) VALUES (?, ?, ?, ?, ?, ?)"
	initiatedBy := sql.NullString{String: t.InitiatedBy, Valid: t.InitiatedBy != ""}
	if _, err := q.tx.ExecContext(ctx, stmt, t.FromAddress, t.ToAddress, t.Amount, t.Reference, t.Source, initiatedBy); err != nil {
		return fmt.Errorf("insert transaction: %w", err)
	}
	return nil
}
