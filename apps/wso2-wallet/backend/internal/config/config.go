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

// Package config loads and validates runtime configuration from the environment.
package config

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

const encryptionKeySize = 32

// defaultCompanyEmailDomain is the organization's email domain unless
// COMPANY_EMAIL_DOMAIN overrides it. Everything limited to people in the
// organization reads it: the initial coin grant and wallet sharing.
//
// Rebrand note: when the organization moves to choreolabs.com, set
// COMPANY_EMAIL_DOMAIN in the deployment, then update this default. Only one domain
// is accepted at a time, and wallets and grants are tied to the email they were
// created with, so people whose address changes need their wallets and grants moved
// to the new address as well.
const defaultCompanyEmailDomain = "wso2.com"

// defaultPeopleEmployeeFilter selects active employees in the HR entity.
const defaultPeopleEmployeeFilter = `{"employeeStatus": ["Active"]}`

// Config is the fully resolved, validated service configuration.
type Config struct {
	CORSAllowedOrigin string
	JWT               JWTConfig
	DB                DBConfig
	EncryptionKey     []byte
	InitialCoins      InitialCoinsConfig
	// CompanyEmailDomain is the organization's email domain.
	CompanyEmailDomain string
	// People is the HR entity connection; nil when it is not configured.
	People      *PeopleConfig
	Maintenance MaintenanceConfig
}

// JWTConfig controls how the caller's token is verified. When JWKSURL is set the
// token's signature, expiry and (if configured) issuer/audience are validated
// against the JWKS. When JWKSURL is empty the service refuses to start unless
// AllowInsecure is explicitly enabled (local development only), in which case
// tokens are decoded but not verified.
type JWTConfig struct {
	JWKSURL       string
	Issuer        string
	Audience      string
	AllowInsecure bool
}

// DBConfig holds the MySQL connection settings.
type DBConfig struct {
	Host                  string
	Port                  string
	User                  string
	Password              string
	Name                  string
	MaxOpenConns          int
	MaxIdleConns          int
	ConnMaxLifetimeSecond int
	ConnectTimeoutSecond  int
}

// InitialCoinsConfig controls the one-time allocation granted on a user's first wallet.
type InitialCoinsConfig struct {
	Enabled       bool
	Amount        string
	FundingWallet string
	EmailDomain   string
}

// PeopleConfig is how to reach the HR entity GraphQL service, used to look up
// employees when sharing a wallet.
type PeopleConfig struct {
	Endpoint     string
	TokenURL     string
	ClientID     string
	ClientSecret string
	Scopes       string
	// Filter is the GraphQL EmployeeFilter that selects active employees.
	Filter map[string]any
}

// MaintenanceConfig controls the client-facing maintenance screen.
type MaintenanceConfig struct {
	Mode    bool
	Message string
}

// DSN returns the MySQL data source name.
func (d DBConfig) DSN() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?parseTime=true&timeout=%ds&tls=preferred",
		d.User, d.Password, d.Host, d.Port, d.Name, d.ConnectTimeoutSecond)
}

// Load reads configuration from the environment and fails on missing required values.
// A local .env file, when present, seeds any unset variables.
func Load() (Config, error) {
	_ = godotenv.Load()

	dbUser, err := mustEnv("DB_USER")
	if err != nil {
		return Config{}, err
	}
	dbName, err := mustEnv("DB_NAME")
	if err != nil {
		return Config{}, err
	}
	key, err := loadEncryptionKey()
	if err != nil {
		return Config{}, err
	}
	people, err := loadPeople()
	if err != nil {
		return Config{}, err
	}
	companyDomain := strings.TrimSpace(envOrDefault("COMPANY_EMAIL_DOMAIN", defaultCompanyEmailDomain))
	if old := os.Getenv("INITIAL_COINS_EMAIL_DOMAIN"); old != "" {
		slog.Warn("INITIAL_COINS_EMAIL_DOMAIN is no longer read; set COMPANY_EMAIL_DOMAIN instead",
			"ignoredValue", old, "companyEmailDomain", companyDomain)
	}
	initialCoins, err := loadInitialCoins(companyDomain)
	if err != nil {
		return Config{}, err
	}

	return Config{
		CORSAllowedOrigin: os.Getenv("CORS_ALLOWED_ORIGIN"),
		JWT: JWTConfig{
			JWKSURL:       os.Getenv("JWT_JWKS_URL"),
			Issuer:        os.Getenv("JWT_ISSUER"),
			Audience:      os.Getenv("JWT_AUDIENCE"),
			AllowInsecure: envBool("JWT_ALLOW_INSECURE", false),
		},
		DB: DBConfig{
			Host:                  envOrDefault("DB_HOST", "localhost"),
			Port:                  envOrDefault("DB_PORT", "3306"),
			User:                  dbUser,
			Password:              os.Getenv("DB_PASSWORD"),
			Name:                  dbName,
			MaxOpenConns:          envIntOrDefault("DB_MAX_OPEN_CONNECTIONS", 25),
			MaxIdleConns:          envIntOrDefault("DB_MAX_IDLE_CONNECTIONS", 25),
			ConnMaxLifetimeSecond: envIntOrDefault("DB_CONN_MAX_LIFETIME_SECONDS", 300),
			ConnectTimeoutSecond:  envIntOrDefault("DB_CONNECT_TIMEOUT_SECONDS", 10),
		},
		EncryptionKey:      key,
		InitialCoins:       initialCoins,
		CompanyEmailDomain: companyDomain,
		People:             people,
		Maintenance: MaintenanceConfig{
			Mode:    envBool("MAINTENANCE_MODE", false),
			Message: os.Getenv("MAINTENANCE_MESSAGE"),
		},
	}, nil
}

func loadEncryptionKey() ([]byte, error) {
	raw, err := mustEnv("ENCRYPTION_KEY")
	if err != nil {
		return nil, err
	}
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("ENCRYPTION_KEY must be base64: %w", err)
	}
	if len(key) != encryptionKeySize {
		return nil, fmt.Errorf("ENCRYPTION_KEY must decode to %d bytes, got %d", encryptionKeySize, len(key))
	}
	return key, nil
}

// loadInitialCoins reads the initial coin grant settings. The grant goes to people
// in the company domain.
func loadInitialCoins(companyDomain string) (InitialCoinsConfig, error) {
	enabled := envBool("INITIAL_COINS_ENABLED", true)
	cfg := InitialCoinsConfig{
		Enabled:     enabled,
		Amount:      envOrDefault("INITIAL_COINS_AMOUNT", "10"),
		EmailDomain: companyDomain,
	}
	if !enabled {
		return cfg, nil
	}
	funding, err := mustEnv("INITIAL_COINS_FUNDING_WALLET")
	if err != nil {
		return InitialCoinsConfig{}, err
	}
	cfg.FundingWallet = funding
	return cfg, nil
}

// loadPeople reads the HR entity connection. It is optional as a whole: with no
// PEOPLE_API_URL the employee search is off and people are added by email. Once the
// URL is set the credentials are required, and both URLs must be HTTPS unless they
// point at this machine, because they carry the client secret and access token.
func loadPeople() (*PeopleConfig, error) {
	endpoint := os.Getenv("PEOPLE_API_URL")
	if endpoint == "" {
		return nil, nil
	}
	cfg := &PeopleConfig{Endpoint: endpoint, Scopes: os.Getenv("PEOPLE_SCOPES")}
	var err error
	if cfg.TokenURL, err = mustEnv("PEOPLE_TOKEN_URL"); err != nil {
		return nil, err
	}
	if cfg.ClientID, err = mustEnv("PEOPLE_CLIENT_ID"); err != nil {
		return nil, err
	}
	if cfg.ClientSecret, err = mustEnv("PEOPLE_CLIENT_SECRET"); err != nil {
		return nil, err
	}
	// The filter is JSON because HR entity versions name the "active" filter
	// differently (for example {"isActive": true}).
	filter := envOrDefault("PEOPLE_EMPLOYEE_FILTER", defaultPeopleEmployeeFilter)
	if err := json.Unmarshal([]byte(filter), &cfg.Filter); err != nil || cfg.Filter == nil {
		return nil, fmt.Errorf("PEOPLE_EMPLOYEE_FILTER must be a JSON object")
	}
	for key, raw := range map[string]string{"PEOPLE_API_URL": cfg.Endpoint, "PEOPLE_TOKEN_URL": cfg.TokenURL} {
		if err := requireSecureURL(key, raw); err != nil {
			return nil, err
		}
	}
	return cfg, nil
}

func requireSecureURL(key, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("%s must be a valid URL", key)
	}
	host := u.Hostname()
	local := host == "localhost" || host == "127.0.0.1" || host == "::1"
	if u.Scheme != "https" && !(u.Scheme == "http" && local) {
		return fmt.Errorf("%s must use HTTPS", key)
	}
	return nil
}

func mustEnv(key string) (string, error) {
	v := os.Getenv(key)
	if v == "" {
		return "", fmt.Errorf("required environment variable is not set: %s", key)
	}
	return v, nil
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envIntOrDefault(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func envBool(key string, def bool) bool {
	switch os.Getenv(key) {
	case "true":
		return true
	case "false":
		return false
	default:
		return def
	}
}
