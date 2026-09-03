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

// Package middleware provides the HTTP middleware chain: auth, CORS, correlation,
// request logging and security headers.
package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/MicahParks/jwkset"
	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"

	"github.com/wso2/wso2-coin/apps/wso2-wallet/backend/internal/config"
	"github.com/wso2/wso2-coin/apps/wso2-wallet/backend/internal/response"
)

const (
	// jwksRefreshInterval is how often the JWK Set is re-fetched in the background.
	jwksRefreshInterval = time.Hour

	assertionHeader = "X-Jwt-Assertion"
	healthPath      = "/health"
)

var signingMethods = []string{
	"RS256", "RS384", "RS512",
	"ES256", "ES384", "ES512",
	"PS256", "PS384", "PS512",
}

// UserInfo is the authenticated caller identity.
type UserInfo struct {
	Email   string
	Subject string
}

type contextKey string

const userInfoKey contextKey = "user-info"

// UserInfoFromContext returns the caller identity, or nil when unauthenticated.
func UserInfoFromContext(ctx context.Context) *UserInfo {
	v, _ := ctx.Value(userInfoKey).(*UserInfo)
	return v
}

// WithUserInfo attaches a caller identity to the context.
func WithUserInfo(ctx context.Context, user *UserInfo) context.Context {
	return context.WithValue(ctx, userInfoKey, user)
}

type jwtClaims struct {
	Email string `json:"email"`
	jwt.RegisteredClaims
}

// Verifier authenticates caller tokens. With a JWKS configured it validates the
// signature, expiry and (when set) issuer and audience; without one it only
// decodes the token.
type Verifier struct {
	keyfunc  jwt.Keyfunc
	verified bool
	issuer   string
	audience string
}

// NewVerifier builds a Verifier from the JWT config. When cfg.JWKSURL is empty it
// returns a decode-only verifier intended for local development behind a trusted
// gateway; Verified reports false in that case so callers can warn.
func NewVerifier(ctx context.Context, cfg config.JWTConfig) (*Verifier, error) {
	if cfg.JWKSURL == "" {
		if !cfg.AllowInsecure {
			return nil, fmt.Errorf("JWT_JWKS_URL is required; set JWT_ALLOW_INSECURE=true only for local development behind a trusted gateway")
		}
		return &Verifier{}, nil
	}
	// SkipAll disables jwkset's JWK validation. Asgardeo publishes x5t#S256 as
	// base64url of the hex digest rather than of the raw bytes, so the thumbprint
	// check rejects its only key and leaves the set empty. Signatures are still
	// verified: the RSA key is built from n/e, which is byte-identical to the one
	// inside x5c.
	store, err := jwkset.NewStorageFromHTTP(cfg.JWKSURL, jwkset.HTTPClientStorageOptions{
		Ctx:             ctx,
		RefreshInterval: jwksRefreshInterval,
		ValidateOptions: jwkset.JWKValidateOptions{SkipAll: true},
	})
	if err != nil {
		return nil, fmt.Errorf("load JWKS from %s: %w", cfg.JWKSURL, err)
	}
	// An empty set verifies nothing, and every request would 401 while the service
	// still looked healthy. Fail to start instead.
	keys, err := store.KeyReadAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("read JWKS from %s: %w", cfg.JWKSURL, err)
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("JWKS at %s contained no usable keys", cfg.JWKSURL)
	}
	jwks, err := keyfunc.New(keyfunc.Options{Storage: store})
	if err != nil {
		return nil, fmt.Errorf("build key function from %s: %w", cfg.JWKSURL, err)
	}
	return &Verifier{
		keyfunc:  jwks.Keyfunc,
		verified: true,
		issuer:   cfg.Issuer,
		audience: cfg.Audience,
	}, nil
}

// Verified reports whether tokens are signature-verified.
func (v *Verifier) Verified() bool { return v.verified }

func (v *Verifier) parse(token string) (*jwtClaims, error) {
	claims := &jwtClaims{}
	if v.keyfunc == nil {
		if _, _, err := jwt.NewParser().ParseUnverified(token, claims); err != nil {
			return nil, err
		}
		return claims, nil
	}
	opts := []jwt.ParserOption{
		jwt.WithValidMethods(signingMethods),
		jwt.WithExpirationRequired(),
	}
	if v.issuer != "" {
		opts = append(opts, jwt.WithIssuer(v.issuer))
	}
	if v.audience != "" {
		opts = append(opts, jwt.WithAudience(v.audience))
	}
	if _, err := jwt.NewParser(opts...).ParseWithClaims(token, claims, v.keyfunc); err != nil {
		return nil, err
	}
	return claims, nil
}

// Auth verifies the caller's token and attaches the identity to the request
// context. Behind the Choreo gateway the token arrives in X-Jwt-Assertion.
func Auth(v *Verifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == healthPath {
				next.ServeHTTP(w, r)
				return
			}

			token := extractToken(r)
			if token == "" {
				response.WriteError(w, http.StatusUnauthorized, response.ErrMsgUnauthorized)
				return
			}

			claims, err := v.parse(token)
			if err != nil {
				response.WriteError(w, http.StatusUnauthorized, response.ErrMsgUnauthorized)
				return
			}
			email := strings.TrimSpace(claims.Email)
			if email == "" {
				response.WriteError(w, http.StatusUnauthorized, response.ErrMsgUnauthorized)
				return
			}

			user := &UserInfo{Email: email, Subject: claims.Subject}
			next.ServeHTTP(w, r.WithContext(WithUserInfo(r.Context(), user)))
		})
	}
}

func extractToken(r *http.Request) string {
	if t := r.Header.Get(assertionHeader); t != "" {
		return t
	}
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimPrefix(auth, "Bearer ")
	}
	return ""
}
