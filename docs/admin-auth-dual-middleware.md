# Admin Auth — Dual Middleware Architecture

The Odoo MCP server has two separate authentication systems and middleware chains:

1. **Dex OIDC middleware** (`internal/auth/middleware.go`) — validates Bearer tokens issued by
   Dex (an OIDC provider). Used for `/mcp`, `/register-key`, and all user-facing endpoints.
2. **Admin HS256 JWT middleware** (`internal/admin/auth.go`) — validates custom HS256-signed JWTs
   issued by the admin login endpoint. Used for `/admin/users`, `/admin/users/{user_id}`, and
   future admin API endpoints.

## Why two auth systems?

The admin login endpoint (`POST /admin/login`) accepts an Odoo API key and returns a custom
HS256-signed JWT. This JWT is **not** a Dex OIDC token — it has no `iss` claim matching Dex,
and Dex has no JWKS entry for it. Previously, the admin API endpoints were protected by the Dex
OIDC middleware, which rejected the admin JWT as invalid.

The fix adds an `AdminAuthMiddleware` that validates the HS256 JWT using the same secret used
to sign it (`jwtSign`/`jwtVerify` use the same HMAC-SHA256 algorithm).

## How it works

### Login flow
1. Admin user enters Odoo API key in the admin web UI
2. `POST /admin/login` validates the key against Odoo's `res.users/context_get`
3. On success, it returns a signed HS256 JWT (24h expiry) with claims: `sub`, `email`, `name`
4. The JavaScript stores the token in `localStorage` under `odoo_mcp_admin_token`

### API call flow
1. JavaScript reads token from `localStorage` and sends it as `Authorization: Bearer <token>`
2. `AdminAuthMiddleware` extracts the token using `extractBearerToken`
3. `jwtVerify` validates the HS256 signature, decodes the payload, and checks expiry
4. On success, the handler proceeds; on failure, returns 401

### Middleware chain per route

| Route | Middleware | Reason |
|-------|-----------|--------|
| `GET /admin` | None | Static HTML page |
| `GET /admin/` | None | Static HTML page |
| `POST /admin/login` | None | Chicken-and-egg — must be public to get a token |
| `GET /admin/users` | `AdminAuthMiddleware` | Admin-only |
| `POST /admin/users` | `AdminAuthMiddleware` | Admin-only |
| `DELETE /admin/users/{user_id}` | `AdminAuthMiddleware` | Admin-only |
| `/mcp` | Dex OIDC middleware | End-user MCP sessions |
| `/register-key` | Dex OIDC middleware | End-user key registration |
| `/.well-known/oauth-protected-resource` | None | OAuth discovery (RFC 9728) |
| `/health` | None | K8s probes |

## Key files

- `internal/admin/auth.go` — `AdminAuthMiddleware`, `jwtSign`, `jwtVerify`, `extractBearerToken`, `Login` handler, `loadJWTSecret`
- `internal/admin/handler.go` — `ListUsers`, `AddUser`, `DeleteUser`, `RegisterRoutes`
- `cmd/odoo-mcp/main.go` — Route wiring, adminAuthMW assignment
- `internal/auth/middleware.go` — Dex OIDC middleware (unchanged)

## JWT secret sources

The JWT secret is loaded by `loadJWTSecret()` in priority order:
1. `KEY_STORE_ENCRYPTION_KEY` (if 64 hex chars, decoded to 32 bytes)
2. `ADMIN_JWT_SECRET` (raw string)
3. Dev fallback: `"odoo-mcp-admin-dev-jwt-secret-32bytes!"`

## Gotchas

- The admin HTML already sends `Authorization: Bearer <token>` via the `apiHeaders()` function —
  no changes were needed to the front end.
- `extractBearerToken` checks for `"Bearer "` prefix (capital B). The admin login sets the
  header as `"bearer "` (lowercase) for the Odoo API key call — this is intentional and distinct.
- If the token's `exp` claim is missing or not a float64, `jwtVerify` treats it as expired.