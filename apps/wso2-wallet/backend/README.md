# WSO2 Wallet Backend

REST service for the WSO2 Wallet microapp. It manages a user's wallets, balances and
transfers, backed by MySQL. Stored balances and transaction amounts are encrypted at the
application layer (AES-256-GCM). Deployed on Choreo; the caller's identity is taken from
the gateway-supplied `X-Jwt-Assertion` header.

## Requirements

- Go 1.25+
- MySQL 8.0+

## Setup

```bash
# 1. Apply the schema.
mysql -h <host> -u <user> -p <database> < resources/schema.sql

# 2. Configure.
cp .env.example .env   # fill in DB_*, ENCRYPTION_KEY, INITIAL_COINS_FUNDING_WALLET

# 3. Run.
go run ./cmd/server
```

## Configuration

| Variable | Required | Default | Description |
|---|---|---|---|
| `CORS_ALLOWED_ORIGIN` | no | — | Allowed CORS origin; empty denies cross-origin requests |
| `JWT_JWKS_URL` | no | — | JWKS endpoint for token verification; required unless `JWT_ALLOW_INSECURE=true` |
| `JWT_ISSUER` | no | — | Expected token issuer (`iss`), checked when set |
| `JWT_AUDIENCE` | no | — | Expected token audience (`aud`), checked when set |
| `JWT_ALLOW_INSECURE` | no | `false` | Allow startup without a JWKS (decode-only); local development only |
| `DB_HOST` / `DB_PORT` | no | `localhost` / `3306` | MySQL host/port |
| `DB_USER` | yes | — | MySQL user |
| `DB_PASSWORD` | no | — | MySQL password |
| `DB_NAME` | yes | — | Database name |
| `ENCRYPTION_KEY` | yes | — | AES-256 key, 32 bytes base64 (`openssl rand -base64 32`) |
| `INITIAL_COINS_ENABLED` | no | `true` | Grant coins on a user's first wallet |
| `INITIAL_COINS_AMOUNT` | no | `10` | Amount to grant |
| `INITIAL_COINS_FUNDING_WALLET` | yes (if enabled) | — | Wallet the grant is debited from |
| `INITIAL_COINS_EMAIL_DOMAIN` | no | `wso2.com` | Domain eligible for the grant |
| `SHARING_EMAIL_DOMAIN` | no | `wso2.com` | Domain both the owner and the other person must belong to for wallet sharing |
| `MAINTENANCE_MODE` | no | `false` | Show the client maintenance screen |
| `MAINTENANCE_MESSAGE` | no | — | Message shown when maintenance mode is on |

## Endpoints

| Method | Path | Description |
|---|---|---|
| `GET` | `/health` | Service liveness |
| `GET` | `/api/v1/wallets` | List the caller's own wallets and the wallets shared with them |
| `POST` | `/api/v1/wallets` | Create a wallet |
| `POST` | `/api/v1/wallets/{address}/set-primary` | Set the default wallet |
| `GET` | `/api/v1/wallets/{address}/balance` | Wallet balance |
| `GET` | `/api/v1/wallets/{address}/transactions` | Wallet history (paginated) |
| `GET` | `/api/v1/wallets/{address}/health` | Wallet integrity |
| `GET` | `/api/v1/wallets/{address}/info` | Wallet name, for someone sending to it |
| `POST` | `/api/v1/wallets/{address}/name` | Set or clear the wallet name (owner) |
| `GET` | `/api/v1/wallets/{address}/access` | List people with access (owner) |
| `POST` | `/api/v1/wallets/{address}/access` | Grant or change access (owner) |
| `POST` | `/api/v1/wallets/{address}/access/revoke` | Remove a person's access (owner) |
| `POST` | `/api/v1/wallets/{address}/access/leave` | Give up your own access to a shared wallet |
| `POST` | `/api/v1/transfers` | Transfer between wallets |

See `openapi.yaml` for the full contract.

## Shared wallets

The owner of a wallet can give other people in the organization access to it, so a
team can run one wallet together.

- **`READ`:** see the balance and history.
- **`TRANSACT`:** also send coins from the wallet.
- **Owner only:** name the wallet, manage access and set it as their default wallet.

A wallet can be named only while it is shared with at least one other person, and the
name is shown only while that holds. Each transfer records who made it; the sending
wallet's history shows it.

Access rows, the wallet name and the transfer initiator are encrypted with
`ENCRYPTION_KEY` and bound to their wallet, user or transaction. A row inserted or
edited directly in the database fails that check and is ignored. The wallet owner
(`user_wallet.user_email`) is not protected this way.

Existing databases need `resources/migration_add_shared_wallets.sql`. It only adds
nullable columns and one table, so it can be applied before this version is deployed.

## Layout

```
cmd/server         entrypoint + server wiring
internal/config    environment configuration
internal/crypto    AES-256-GCM field encryption
internal/money     fixed-point amount math
internal/middleware auth, CORS, correlation, logging, security headers
internal/model     API request/response types
internal/wallet    handler -> service -> repository
resources/         schema
```

## Test

```bash
go test -race ./...
```
