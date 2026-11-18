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

// Package migrate applies the numbered SQL migrations and records each one in
// swag_applied_migrations. It runs only from make migrate (cmd/migrate) and the
// integration tests, with schema-owner rights; the server only checks.
package migrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"

	"github.com/go-sql-driver/mysql"
)

// errNoSuchTable is MySQL's ER_NO_SUCH_TABLE.
const errNoSuchTable = 1146

// ErrSchemaBehind means the newest migration is not applied yet.
var ErrSchemaBehind = errors.New("database schema is behind; run make migrate")

var fileRe = regexp.MustCompile(`^(\d{4})_[a-z0-9_]+\.sql$`)

const trackingTable = `CREATE TABLE IF NOT EXISTS swag_applied_migrations (
    filename   VARCHAR(255) NOT NULL,
    applied_at DATETIME(6)  NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (filename)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci`

// Files lists the migration files in order. Any other *.sql name, or two files
// with one number, is an error.
func Files(fsys fs.FS) ([]string, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	seen := map[string]string{}
	var files []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || len(name) < 4 || name[len(name)-4:] != ".sql" {
			continue
		}
		m := fileRe.FindStringSubmatch(name)
		if m == nil {
			return nil, fmt.Errorf("migration %q: name must be NNNN_lower_snake.sql", name)
		}
		if prev, dup := seen[m[1]]; dup {
			return nil, fmt.Errorf("migrations %q and %q share number %s", prev, name, m[1])
		}
		seen[m[1]] = name
		files = append(files, name)
	}
	sort.Strings(files)
	return files, nil
}

// Latest returns the newest migration file name.
func Latest(fsys fs.FS) (string, error) {
	files, err := Files(fsys)
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "", errors.New("no migration files")
	}
	return files[len(files)-1], nil
}

// lockName serialises migration runs on one database.
const lockName = "swag_store_migrate"

// lockWait is how long a run waits for another run to finish.
const lockWait = 300

// Apply runs every file not yet recorded, in order, and records each one
// after its statements succeed. The connection must allow multiple statements
// (multiStatements=true). It is safe to re-run, and concurrent runs wait for
// each other on a named lock.
//
// MySQL commits each DDL statement on its own, so a run stopped inside a file
// leaves part of it applied and the file unrecorded. Every file must therefore
// be safe to replay (CREATE TABLE IF NOT EXISTS, and so on): the next run
// replays the unrecorded file and finishes it.
func Apply(ctx context.Context, db *sql.DB, fsys fs.FS) ([]string, error) {
	// GET_LOCK belongs to the session, so every statement runs on one connection.
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	var got sql.NullInt64
	if err := conn.QueryRowContext(ctx, "SELECT GET_LOCK(?, ?)", lockName, lockWait).Scan(&got); err != nil {
		return nil, fmt.Errorf("take migration lock: %w", err)
	}
	if got.Int64 != 1 {
		return nil, errors.New("take migration lock: another migration run still holds it")
	}
	defer func() {
		_, _ = conn.ExecContext(context.WithoutCancel(ctx), "DO RELEASE_LOCK(?)", lockName)
	}()
	return apply(ctx, conn, fsys)
}

func apply(ctx context.Context, conn *sql.Conn, fsys fs.FS) ([]string, error) {
	if _, err := conn.ExecContext(ctx, trackingTable); err != nil {
		return nil, fmt.Errorf("create tracking table: %w", err)
	}
	done, err := appliedSet(ctx, conn)
	if err != nil {
		return nil, err
	}
	files, err := Files(fsys)
	if err != nil {
		return nil, err
	}
	var applied []string
	for _, name := range files {
		if done[name] {
			continue
		}
		body, err := fs.ReadFile(fsys, name)
		if err != nil {
			return applied, err
		}
		if _, err := conn.ExecContext(ctx, string(body)); err != nil {
			return applied, fmt.Errorf("apply %s: %w", name, err)
		}
		if _, err := conn.ExecContext(ctx, "INSERT INTO swag_applied_migrations (filename) VALUES (?)", name); err != nil {
			return applied, fmt.Errorf("record %s: %w", name, err)
		}
		applied = append(applied, name)
	}
	return applied, nil
}

func appliedSet(ctx context.Context, conn *sql.Conn) (map[string]bool, error) {
	rows, err := conn.QueryContext(ctx, "SELECT filename FROM swag_applied_migrations")
	if err != nil {
		return nil, fmt.Errorf("read applied migrations: %w", err)
	}
	defer rows.Close()
	done := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		done[name] = true
	}
	return done, rows.Err()
}

// CheckCurrent returns ErrSchemaBehind unless the newest embedded migration is
// recorded. It never changes the schema.
func CheckCurrent(ctx context.Context, db *sql.DB, fsys fs.FS) error {
	latest, err := Latest(fsys)
	if err != nil {
		return err
	}
	var n int
	err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM swag_applied_migrations WHERE filename = ?", latest).Scan(&n)
	if err != nil {
		var myErr *mysql.MySQLError
		if errors.As(err, &myErr) && myErr.Number == errNoSuchTable {
			return fmt.Errorf("%w (no swag_applied_migrations table)", ErrSchemaBehind)
		}
		return fmt.Errorf("read applied migrations: %w", err)
	}
	if n == 0 {
		return ErrSchemaBehind
	}
	return nil
}
