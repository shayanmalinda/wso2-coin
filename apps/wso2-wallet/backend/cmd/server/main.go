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

// Command server runs the WSO2 Wallet backend HTTP service.
//
// Allow X.509 certificates with negative serial numbers so JWKS documents that
// embed such certs (e.g. Asgardeo's) still parse under Go 1.23+.
//
//go:debug x509negativeserial=1
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/wso2/wso2-coin/apps/wso2-wallet/backend/internal/appconfig"
	"github.com/wso2/wso2-coin/apps/wso2-wallet/backend/internal/config"
	"github.com/wso2/wso2-coin/apps/wso2-wallet/backend/internal/crypto"
	"github.com/wso2/wso2-coin/apps/wso2-wallet/backend/internal/middleware"
	"github.com/wso2/wso2-coin/apps/wso2-wallet/backend/internal/wallet"
)

func main() {
	middleware.ConfigureLogger()
	if err := run(); err != nil {
		slog.Error("server terminated", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := sql.Open("mysql", cfg.DB.DSN())
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(cfg.DB.MaxOpenConns)
	db.SetMaxIdleConns(cfg.DB.MaxIdleConns)
	db.SetConnMaxLifetime(time.Duration(cfg.DB.ConnMaxLifetimeSecond) * time.Second)
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}

	enc, err := crypto.New(cfg.EncryptionKey)
	if err != nil {
		return fmt.Errorf("init encryptor: %w", err)
	}

	verifier, err := middleware.NewVerifier(ctx, cfg.JWT)
	if err != nil {
		return fmt.Errorf("init jwt verifier: %w", err)
	}
	if !verifier.Verified() {
		slog.WarnContext(ctx, "JWT signature verification disabled (JWT_JWKS_URL not set); tokens are decoded but not verified")
	}

	svc := wallet.NewService(wallet.NewRepository(db), enc, wallet.InitialCoins{
		Enabled:       cfg.InitialCoins.Enabled,
		Amount:        cfg.InitialCoins.Amount,
		FundingWallet: cfg.InitialCoins.FundingWallet,
		EmailDomain:   cfg.InitialCoins.EmailDomain,
	})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	wallet.NewHandler(svc).RegisterRoutes(mux)
	appconfig.NewHandler(cfg.Maintenance.Mode, cfg.Maintenance.Message).RegisterRoutes(mux)

	root := middleware.SecurityHeaders(
		middleware.CORS(cfg.CORSAllowedOrigin)(
			middleware.CorrelationID(
				middleware.Logger(
					middleware.Auth(verifier)(mux)))))

	srv := &http.Server{
		Addr:              cfg.Port,
		Handler:           root,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		slog.InfoContext(ctx, "server starting", "port", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.ErrorContext(ctx, "server error", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}
