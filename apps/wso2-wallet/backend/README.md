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
| `COMPANY_EMAIL_DOMAIN` | no | `wso2.com` | The organization's email domain. Only its members get the initial coin grant, and wallet sharing requires both the owner and the other person to belong to it. Replaces `INITIAL_COINS_EMAIL_DOMAIN`, which is no longer read |
| `PEOPLE_API_URL` | no | — | HR entity GraphQL endpoint for the employee search. Empty turns the search off |
| `PEOPLE_TOKEN_URL` / `PEOPLE_CLIENT_ID` / `PEOPLE_CLIENT_SECRET` | yes (if `PEOPLE_API_URL` is set) | — | OAuth2 client credentials for the HR entity |
| `PEOPLE_SCOPES` | no | — | Scopes to request for the HR entity token |
| `PEOPLE_EMPLOYEE_FILTER` | no | `{"employeeStatus": ["Active"]}` | GraphQL `EmployeeFilter` (JSON) that selects active employees |
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
| `GET` | `/api/v1/employees?q=` | Search people in the organization to share a wallet with |
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

When the HR entity is configured (`PEOPLE_API_URL`), the owner can pick a person by
name: `GET /api/v1/employees?q=` searches active employees in the company domain. A
search needs at least 3 characters and returns at most 10 people, and only callers
in the company domain may use it. The employee list
is cached for 24 hours, and an older copy is served if the HR entity is down. Without
the HR entity the search returns 503 and people are added by email, as before.

Access rows, the wallet name and the transfer initiator are sealed with
`ENCRYPTION_KEY` and bound to their wallet, user or transaction. The seal covers the
access level (bound to the wallet and person), the name and the initiator. A grant
inserted or edited without the key fails the check and is ignored. The seal does not
cover `granted_by` or `created_on`, and it cannot detect replay: someone who can write
to the database could restore a previously valid grant after it was revoked. The
wallet owner (`user_wallet.user_email`) is not protected this way.

`COMPANY_EMAIL_DOMAIN` is checked when a grant is created or changed. Changing it
later does not remove existing grants; the owner has to revoke them.

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
internal/people    employee search backed by the HR entity
internal/wallet    handler -> service -> repository
resources/         schema
```

## Test

```bash
go test -race ./...
```
