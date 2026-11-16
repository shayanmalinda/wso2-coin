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

// Command migrate applies migrations/*.sql that are not yet recorded in
// swag_applied_migrations (make migrate). It needs schema-owner rights; the
// service's own database user does not have them.
package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"time"

	_ "github.com/go-sql-driver/mysql" // registers the "mysql" driver

	"github.com/wso2/wso2-coin/apps/swag-store/backend/internal/dbconn"
	"github.com/wso2/wso2-coin/apps/swag-store/backend/internal/migrate"
	"github.com/wso2/wso2-coin/apps/swag-store/backend/migrations"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
}

func run() error {
	dsn, err := migrationDSN()
	if err != nil {
		return err
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	applied, err := migrate.Apply(ctx, db, migrations.FS)
	for _, name := range applied {
		fmt.Println("applied", name)
	}
	if err != nil {
		return err
	}
	if len(applied) == 0 {
		fmt.Println("schema is current")
	}
	return nil
}

// migrationDSN is the tools' connection (dbconn.ToolConfig) with
// multi-statement files enabled.
func migrationDSN() (string, error) {
	mc, err := dbconn.ToolConfig(os.Getenv)
	if err != nil {
		return "", err
	}
	mc.MultiStatements = true
	mc.ParseTime = true
	return mc.FormatDSN(), nil
}
