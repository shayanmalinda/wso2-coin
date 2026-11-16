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

// Package dbconn holds the database connection settings shared by the server
// and the command-line tools (migrate, seed), so each rule lives in one place.
package dbconn

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/go-sql-driver/mysql"
)

// tlsName is the name the private-CA TLS config is registered under.
const tlsName = "swag"

// TrustCA registers pem as the only root of a TLS config, with verification on
// and TLS 1.2 at least, and returns the name to put in mysql.Config.TLSConfig.
// pem may carry `\n` escapes, as a one-line environment value does.
func TrustCA(pem string) (string, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(strings.ReplaceAll(pem, `\n`, "\n"))) {
		return "", errors.New("DB_CA_CERT holds no PEM certificate")
	}
	if err := mysql.RegisterTLSConfig(tlsName, &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}); err != nil {
		return "", fmt.Errorf("DB_CA_CERT: %w", err)
	}
	return tlsName, nil
}

// ToolConfig is the connection for the command-line tools: DATABASE_URL as
// given, or DB_HOST, DB_PORT (3306), DB_USER, DB_PASSWORD and DB_NAME with TLS
// on unless DB_TLS=false. With TLS on, DB_CA_CERT is trusted as the server
// trusts it. Callers add the session flags they need.
func ToolConfig(getenv func(string) string) (*mysql.Config, error) {
	var mc *mysql.Config
	if raw := getenv("DATABASE_URL"); raw != "" {
		parsed, err := mysql.ParseDSN(raw)
		if err != nil {
			return nil, fmt.Errorf("DATABASE_URL: %w", err)
		}
		mc = parsed
	} else {
		host, user, name := getenv("DB_HOST"), getenv("DB_USER"), getenv("DB_NAME")
		if host == "" || user == "" || name == "" {
			return nil, errors.New("set DATABASE_URL, or DB_HOST, DB_USER, DB_PASSWORD and DB_NAME")
		}
		port := getenv("DB_PORT")
		if port == "" {
			port = "3306"
		}
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("DB_PORT must be a port number, got %q", port)
		}
		mc = mysql.NewConfig()
		mc.Net, mc.Addr = "tcp", net.JoinHostPort(host, port)
		mc.User, mc.Passwd, mc.DBName = user, getenv("DB_PASSWORD"), name
		if getenv("DB_TLS") != "false" {
			mc.TLSConfig = "true"
		}
	}
	if pem := getenv("DB_CA_CERT"); pem != "" && getenv("DB_TLS") != "false" {
		name, err := TrustCA(pem)
		if err != nil {
			return nil, err
		}
		mc.TLSConfig = name
	}
	return mc, nil
}
