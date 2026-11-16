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

package dbconn

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

func dbVars() map[string]string {
	return map[string]string{"DB_HOST": "db.example", "DB_USER": "owner", "DB_PASSWORD": "pw", "DB_NAME": "swag"}
}

// parsed runs ToolConfig and reads its DSN back, as the driver will.
func parsed(t *testing.T, env map[string]string) *mysql.Config {
	t.Helper()
	mc, err := ToolConfig(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	back, err := mysql.ParseDSN(mc.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	return back
}

func TestToolConfigNeedsADatabase(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"nothing":  {},
		"no user":  {"DB_HOST": "db.example", "DB_NAME": "swag"},
		"bad URL":  {"DATABASE_URL": "::::"},
		"no host":  {"DB_USER": "owner", "DB_NAME": "swag"},
		"bad port": {"DB_HOST": "db.example", "DB_PORT": "80a", "DB_USER": "owner", "DB_NAME": "swag"},
	} {
		if _, err := ToolConfig(func(k string) string { return env[k] }); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestToolConfigReadsDBVars(t *testing.T) {
	mc := parsed(t, dbVars())
	if mc.Addr != "db.example:3306" || mc.User != "owner" || mc.Passwd != "pw" || mc.DBName != "swag" {
		t.Errorf("config = %+v", mc)
	}
	if mc.TLSConfig != "true" {
		t.Errorf("DB_* default TLSConfig = %q, want true", mc.TLSConfig)
	}
}

// DB_CA_CERT is the private CA the server trusts; the tools must trust it too,
// with verification still on.
func TestToolConfigTrustsTheDatabaseCA(t *testing.T) {
	ca := testCA(t)
	for name, pem := range map[string]string{"PEM": ca, "PEM with escaped newlines": strings.ReplaceAll(ca, "\n", `\n`)} {
		t.Run(name, func(t *testing.T) {
			env := dbVars()
			env["DB_CA_CERT"] = pem
			mc := parsed(t, env)
			if mc.TLS == nil || mc.TLS.RootCAs == nil {
				t.Fatalf("TLS = %+v, want the CA as root", mc.TLS)
			}
			if mc.TLS.InsecureSkipVerify || mc.TLS.MinVersion < tls.VersionTLS12 {
				t.Errorf("TLS verify skipped or below 1.2: %+v", mc.TLS)
			}
		})
	}
}

func TestToolConfigRefusesACAWithNoCertificate(t *testing.T) {
	env := dbVars()
	env["DB_CA_CERT"] = "not a certificate"
	if _, err := ToolConfig(func(k string) string { return env[k] }); err == nil {
		t.Fatal("accepted a DB_CA_CERT with no certificate")
	}
}

func TestToolConfigWithoutACAKeepsItsTLSChoice(t *testing.T) {
	env := dbVars()
	env["DB_TLS"], env["DB_CA_CERT"] = "false", testCA(t)
	if mc := parsed(t, env); mc.TLSConfig != "" {
		t.Errorf("DB_TLS=false TLSConfig = %q, want none", mc.TLSConfig)
	}
	local := map[string]string{"DATABASE_URL": "root:pw@tcp(127.0.0.1:3306)/swag"}
	if mc := parsed(t, local); mc.TLSConfig != "" || mc.Addr != "127.0.0.1:3306" {
		t.Errorf("local DATABASE_URL = %+v, want it as given", mc)
	}
}
