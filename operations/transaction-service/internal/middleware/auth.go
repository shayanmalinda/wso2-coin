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
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/MicahParks/jwkset"
	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/time/rate"

	"github.com/wso2/wso2-coin/operations/transaction-service/internal/config"
	"github.com/wso2/wso2-coin/operations/transaction-service/internal/response"
)

const (
	assertionHeader = "X-Jwt-Assertion"
	healthPath      = "/health"

	jwksHTTPTimeout     = 15 * time.Second
	jwksRefreshInterval = time.Hour
	clockSkewLeeway     = time.Minute
)

var signingMethods = []string{
	"RS256", "RS384", "RS512",
	"ES256", "ES384", "ES512",
	"PS256", "PS384", "PS512",
}

type contextKey string

const clientIDKey contextKey = "client-id"

// ClientIDFromContext returns the authenticated caller's client id, or an empty
// string when the request is unauthenticated.
func ClientIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(clientIDKey).(string)
	return v
}

// WithClientID attaches the caller's client id to the context.
func WithClientID(ctx context.Context, clientID string) context.Context {
	return context.WithValue(ctx, clientIDKey, clientID)
}

// jwtClaims carries the standard registered claims; the caller is identified by the
// subject (sub) claim, which is treated as the client id. Email carries the
// end-user email claim used by the payments flow (empty for service tokens).
type jwtClaims struct {
	jwt.RegisteredClaims
	Email string `json:"email,omitempty"`
}

// UserClaims holds the verified claims extracted from an end-user token.
type UserClaims struct {
	Subject string
	Email   string
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
	kf, err := newKeyfunc(ctx, cfg.JWKSURL)
	if err != nil {
		return nil, fmt.Errorf("load JWKS from %s: %w", cfg.JWKSURL, err)
	}
	return &Verifier{
		keyfunc:  kf,
		verified: true,
		issuer:   cfg.Issuer,
		audience: cfg.Audience,
	}, nil
}

// newKeyfunc loads the JWKS at jwksURL into an hourly-refreshing key store and returns
// its key lookup. It differs from keyfunc.NewDefaultCtx in two deliberate ways. The
// first fetch must succeed and yield at least one key, so a wrong URL or an
// unparseable document fails at startup instead of leaving an empty key set that
// rejects every token as unverifiable. And JWK metadata validation is skipped: some
// issuers (Asgardeo) publish x5t#S256 as hex rather than base64url, which the strict
// validator treats as fatal for the whole set. Signatures are still verified against
// the published key material, so authentication strength is unchanged.
func newKeyfunc(ctx context.Context, jwksURL string) (jwt.Keyfunc, error) {
	storage, err := jwkset.NewStorageFromHTTP(jwksURL, jwkset.HTTPClientStorageOptions{
		Ctx:             ctx,
		HTTPTimeout:     jwksHTTPTimeout,
		RefreshInterval: jwksRefreshInterval,
		ValidateOptions: jwkset.JWKValidateOptions{SkipAll: true},
		RefreshErrorHandler: func(ctx context.Context, err error) {
			slog.ErrorContext(ctx, "jwks refresh failed", "url", jwksURL, "err", err)
		},
	})
	if err != nil {
		return nil, err
	}
	keys, err := storage.KeyReadAll(ctx)
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("jwks contains no keys")
	}
	client, err := jwkset.NewHTTPClient(jwkset.HTTPClientOptions{
		HTTPURLs:          map[string]jwkset.Storage{jwksURL: storage},
		RateLimitWaitMax:  time.Minute,
		RefreshUnknownKID: rate.NewLimiter(rate.Every(5*time.Minute), 1),
	})
	if err != nil {
		return nil, err
	}
	kf, err := keyfunc.New(keyfunc.Options{Ctx: ctx, Storage: client})
	if err != nil {
		return nil, err
	}
	return kf.Keyfunc, nil
}

// Verified reports whether tokens are signature-verified.
func (v *Verifier) Verified() bool { return v.verified }

// Verify parses and validates a token and returns its claims. With a JWKS
// configured it checks the signature, expiry and (when set) issuer and audience.
func (v *Verifier) Verify(token string) (UserClaims, error) {
	claims, err := v.parse(token)
	if err != nil {
		return UserClaims{}, err
	}
	return UserClaims{Subject: claims.Subject, Email: claims.Email}, nil
}

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
		// Tolerate small clock differences between the issuer and this host: a
		// freshly minted token is not rejected as "not valid yet", and a token is
		// accepted up to the same margin past its expiry.
		jwt.WithLeeway(clockSkewLeeway),
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

// Auth verifies the caller's token and attaches the client id (the sub claim) to the
// request context. Behind the Choreo gateway the token arrives in X-Jwt-Assertion.
// The token and the client id are never logged.
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
			clientID := strings.TrimSpace(claims.Subject)
			if clientID == "" {
				response.WriteError(w, http.StatusUnauthorized, response.ErrMsgUnauthorized)
				return
			}

			next.ServeHTTP(w, r.WithContext(WithClientID(r.Context(), clientID)))
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
