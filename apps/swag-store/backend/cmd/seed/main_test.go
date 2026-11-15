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

package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

// testCA returns a self-signed CA certificate as PEM.
func testCA(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test db ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func dbVars(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("DB_HOST", "db.example")
	t.Setenv("DB_USER", "swag")
	t.Setenv("DB_PASSWORD", "pw")
	t.Setenv("DB_NAME", "swag")
	t.Setenv("DB_TLS", "")
	t.Setenv("DB_CA_CERT", "")
}

func parsed(t *testing.T) *mysql.Config {
	t.Helper()
	d, err := dsn()
	if err != nil {
		t.Fatal(err)
	}
	mc, err := mysql.ParseDSN(d)
	if err != nil {
		t.Fatal(err)
	}
	return mc
}

// DB_CA_CERT is the private CA the server trusts; seed must trust it too,
// with verification still on.
func TestDSNTrustsTheDatabaseCA(t *testing.T) {
	ca := testCA(t)
	for _, tc := range []struct{ name, pem string }{
		{"PEM", ca},
		{"PEM with escaped newlines", strings.ReplaceAll(ca, "\n", `\n`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dbVars(t)
			t.Setenv("DB_CA_CERT", tc.pem)
			mc := parsed(t)
			if mc.TLS == nil || mc.TLS.RootCAs == nil {
				t.Fatalf("TLS = %+v, want the CA as root", mc.TLS)
			}
			if mc.TLS.InsecureSkipVerify || mc.TLS.MinVersion < tls.VersionTLS12 {
				t.Errorf("TLS verify skipped or below 1.2: %+v", mc.TLS)
			}
		})
	}
}

func TestDSNRefusesACAWithNoCertificate(t *testing.T) {
	dbVars(t)
	t.Setenv("DB_CA_CERT", "not a certificate")
	if _, err := dsn(); err == nil {
		t.Fatal("dsn accepted a DB_CA_CERT with no certificate")
	}
}

func TestDSNWithoutACAKeepsItsTLSChoice(t *testing.T) {
	dbVars(t)
	if mc := parsed(t); mc.TLSConfig != "true" {
		t.Errorf("DB_* default TLSConfig = %q, want true", mc.TLSConfig)
	}
	t.Setenv("DB_TLS", "false")
	t.Setenv("DB_CA_CERT", testCA(t))
	if mc := parsed(t); mc.TLSConfig != "" {
		t.Errorf("DB_TLS=false TLSConfig = %q, want none", mc.TLSConfig)
	}
	dbVars(t)
	t.Setenv("DATABASE_URL", "root:pw@tcp(127.0.0.1:3306)/swag")
	if mc := parsed(t); mc.TLSConfig != "" {
		t.Errorf("local DATABASE_URL TLSConfig = %q, want none", mc.TLSConfig)
	}
}
