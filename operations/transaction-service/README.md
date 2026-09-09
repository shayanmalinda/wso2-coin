# Transaction Service

Client-scoped token-operations API. Each API client has a master (sender) wallet; the
service dispenses coins from it and lets clients query balances and transactions, backed by
MySQL. Stored balances and transaction amounts are encrypted at the application layer
(AES-256-GCM). Deployed on Choreo; the caller's identity is taken from the gateway-supplied
`X-Jwt-Assertion` header, and the master-wallet endpoints resolve the client from the token's
`sub` claim.

## Requirements

- Go 1.25+
- MySQL 8.0+

## Setup

```bash
# 1. Apply the schema.
mysql -h <host> -u <user> -p <database> < resources/schema.sql

# 2. Configure.
cp .env.example .env   # fill in DB_*, ENCRYPTION_KEY

# 3. Run.
go run ./cmd/server
```

## Configuration

| Variable | Required | Default | Description |
|---|---|---|---|
| `PORT` | no | `8080` | HTTP listen port |
| `CORS_ALLOWED_ORIGIN` | no | — | Allowed CORS origin; empty denies cross-origin requests |
| `JWT_JWKS_URL` | no | — | JWKS endpoint for token verification; required unless `JWT_ALLOW_INSECURE=true` |
| `JWT_ISSUER` | no | — | Expected token issuer (`iss`), checked when set |
| `JWT_AUDIENCE` | no | — | Expected token audience (`aud`), checked when set |
| `JWT_ALLOW_INSECURE` | no | `false` | Allow startup without a JWKS (decode-only); local development only |
| `USER_JWT_JWKS_URL` | no | `JWT_JWKS_URL` | JWKS for the end-user token (`X-User-Assertion`); defaults to `JWT_JWKS_URL`. Enables `POST /payments` and `GET /wallets/me` |
| `USER_JWT_ISSUER` | no | `JWT_ISSUER` | Expected end-user token issuer (`iss`), checked when set |
| `USER_JWT_AUDIENCE` | no | `JWT_AUDIENCE` | Expected end-user token audience (`aud`), checked when set |
| `USER_JWT_ALLOW_INSECURE` | no | `JWT_ALLOW_INSECURE` | Decode the end-user token without verifying its signature; local development only |
| `DB_HOST` / `DB_PORT` | no | `localhost` / `3306` | MySQL host/port |
| `DB_USER` | yes | — | MySQL user |
| `DB_PASSWORD` | no | — | MySQL password |
| `DB_NAME` | yes | — | Database name |
| `ENCRYPTION_KEY` | yes | — | AES-256 key, 32 bytes base64 (`openssl rand -base64 32`); must match the wallet backend / migration |

## Endpoints

| Method | Path | Description | Client `sub` |
|---|---|---|---|
| `GET` | `/health` | Service liveness | — |
| `GET` | `/wallets` | List all wallets (address, default flag, created) | — |
| `GET` | `/wallets/addresses` | List all distinct wallet addresses | — |
| `GET` | `/wallets/master/balance` | Caller's master wallet balance | required |
| `POST` | `/wallets/master/transfer` | Transfer from the caller's master wallet; optional `reference` is the idempotency key (replay → 200, mismatch → 409) | required |
| `GET` | `/wallets/{address}/balance` | Any wallet's balance | — |
| `POST` | `/transactions/search` | Browse and filter transactions | — |
| `GET` | `/transactions/{reference}` | Get a single transaction | — |

The master-wallet endpoints resolve the calling client from the token's `sub` claim (client
ID); the query endpoints require only a valid token. See `openapi.yaml` for the full contract.

## Test

```bash
go test -race ./...
```
