# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**Gofun** — event ticketing platform (Go + Vue 3). Normal purchase and rush-sale execute use Redis quota pre-deduct, transactional outbox, and RabbitMQ async order finalization. See `docs/FUCHANG_PHASE1.md` for scope; `interview-prep/00_CONTEXT_LOCK.md` for interview facts.

支付目前只实现内置 sandbox provider；以下说明以当前票务代码为准，不能把沙箱写成真实支付渠道。

## Development Commands

### Backend (Go 1.25)

All commands run from `backend/`:

```bash
# Build
cd backend && go build -o gofun-server .

# Run (requires running MySQL, Redis, RabbitMQ)
go run . -config ./config/config.yaml

# Run all tests
go test ./... -count=1

# Run a single test (tests are in models/, repository/, service/, tests/unit/)
go test ./models/ -run TestOrderStatus -v
go test ./tests/unit/ -run TestResponseFormat -v
go test ./service/ -run TestCreateOrder -v

# Run tests with coverage
go test ./... -coverprofile=coverage.out
```

### Frontend (Vue 3 + Vite)

```bash
cd frontend && npm.cmd install && npm.cmd run dev # dev server on :5173
cd frontend && npm.cmd run build                  # production build → dist/
```

### Docker Compose (full stack)

```bash
cp deploy/env.example .env && nano .env          # set passwords first
docker compose up -d --build
docker compose logs -f backend
```

### Load testing (`tests/load/`)

Node.js scripts (built-in `fetch`, no k6/wrk needed) that pressure a running backend. Config is via env vars read in `tests/load/lib/load_common.mjs` (`BASE_URL`, `CONCURRENCY`, `DURATION_SECONDS`, `ADMIN_PASSWORD`, etc.).

```bash
node tests/load/ticket_smoke.mjs
node tests/load/ticket_rush_spike.mjs
node tests/integration/rush_concurrency.mjs
```

Write scenarios mutate real data — use a dedicated DB/Redis/queue. See `tests/load/results/capacity-buckets-compare-20260731.md`.

### Monitoring stack

`monitoring/docker-compose.monitoring.yml` runs Prometheus + Grafana separately from the app stack, scraping the backend's `/metrics` endpoint. Grafana dashboard: `monitoring/grafana/dashboards/gofun_ticketing_overview.json`.

## Architecture

### Layered backend

```
controller → service → repository → models (GORM)
                 ↓
          container (DI: DB, Redis, MQ, Snowflake, repos)
```

- **`container/`** — DI container that wires DB, Redis, RabbitMQ, Snowflake, and repositories. Also sets compatibility `common` package globals for middleware.
- **`common/`** — Compatibility globals (`common.DB`, `common.RDB`, `common.MQChannel`, `common.Node`) plus auth middleware, JWT, rate limiting, and RabbitMQ helpers. New code prefers `container.Container`.
- **`controller/`** — Gin handlers. Thin: validate input, call service, return response.
- **`service/`** — Business logic. Services receive `*container.Container` or individual dependencies.
- **`repository/`** — Interface-based data access over GORM. Ticket catalog and ticket order repositories are defined alongside implementations.
- **`models/`** — GORM model structs with soft-delete via `gorm.DeletedAt`. Includes order state machine (`CanTransitionTo`).
- **`pkg/`** — Reusable packages: `response` (JSON response helpers + error codes), `apperr` (typed app errors), `lock` (Redis distributed lock), `logger` (zap + lumberjack), `middleware` (request ID), `validator` (custom validations).
- **`metrics/`** — Prometheus metrics exposed at `/metrics`, with middleware tracking HTTP requests, ticket-order events, payment, verification, and MQ messages.
- **`config/`** — Viper-based config with YAML file + env var overrides. `GlobalConfig` singleton.

### Order flow (async)

```
User request → controller → TicketOrderService
  → request validation and idempotency check
  → Redis Lua pre-deducts ticket-tier quota
  → writes ticket-order Outbox row
  → publisher-confirmed RabbitMQ delivery
  → returns queued / processing state

Consumer:
  → DB transaction creates TicketOrder (pending_payment) and order items
  → publishes payment-timeout message
  → timeout worker conditionally cancels still-pending orders

Payment:
  → creates or reuses a payment transaction
  → SandboxPaymentGateway schedules an asynchronous signed callback
  → callback validates signature, amount, provider and transaction state
  → DB transaction marks payment and order paid
  → paid order issues electronic tickets
```

### Order state machine

Ticket order states are `queued`, `pending_payment`, `paid`, `cancelled`, and `refunded`; payment states are `pending`, `paid`, `failed`, and `refunded`.

Order/payment transitions are guarded by service checks and database conditions. A paid order can issue and later revoke tickets; a pending order must not issue tickets.

### Background goroutines (started in main.go)

| Goroutine | Interval | Purpose |
|-----------|----------|---------|
| Ticket-order consumer | event-driven (RabbitMQ) | Persists queued ticket orders |
| Outbox publisher | configured workers | Publishes pending rows with confirm and recovery |
| Payment-timeout consumer | event-driven (RabbitMQ) | Cancels expired pending-payment orders |
| Inventory compensation | periodic | Reconciles Redis quota against MySQL |
| IP limiter cleanup | 30 min | Removes stale per-IP rate limiters |

### Order timeout (delayed queue)

```
Ticket order service publishes a delayed payment-timeout message.
When it expires, the timeout worker conditionally cancels the still-pending order
and restores quota. Payment and timeout may race, so the database condition is
the final guard.
```

### Inventory compensation

Runs periodically for ticket tiers. MySQL remains the final source of truth;
compensation only repairs missing or over-reserved Redis quota.

### Authentication

JWT with access + refresh tokens. `AuthMiddleware` validates `Authorization: Bearer <token>` and sets `user_id` in Gin context. `AdminAuthMiddleware` checks role="admin" via DB query. Frontend uses axios interceptor for automatic token refresh on 401.

### Middleware chain (in order)

1. `RequestIDMiddleware` — attaches UUID per request
2. `PrometheusMiddleware` — tracks request count/duration
3. `GlobalRateLimitMiddleware` — token-bucket rate limiter (100k rps in config)
4. `IPRateLimitMiddleware` — per-IP token-bucket limiter, cleaned every 30min
5. CORS — configured allow origins from config

### Frontend

**Routing**: Vue Router with lazy-loaded routes. Route guards in `router/index.js`:
- No token → redirect to `/login` (except auth pages)
- Has token + on auth page → redirect to `/`
- `meta.requiresAdmin` + role ≠ admin → redirect to `/`

**State**: Cart is a reactive Pinia-style composable (`stores/cart.js`) — local only, not persisted to server. IDs from API come as strings (`json:",string"` tag) and are converted to numbers for cart operations.

**API layer** (`api/index.js`): Axios instance with `/api/v1` base. Request interceptor attaches `Authorization` header from localStorage. Response interceptor on 401:
- If not a refresh request and `refresh_token` exists → POST `/auth/refresh` (deduplicated via `refreshPromise` singleton), retry original request with new token
- If still 401 after refresh (or no refresh token) → clear auth storage, redirect to `/login`

**Idempotency keys**: Generated client-side via `crypto.randomUUID()` (fallback to `Date.now()-Math.random()`). Sent as `idempotency_key` on order creation to prevent duplicate submissions.

**Dev proxy**: Vite proxies `/api` → `http://127.0.0.1:8080` (see `vite.config.js`), so frontend dev server avoids CORS issues.

## Key Conventions

- **Error codes**: 5-digit business codes: 4xxxx client errors, 5xxxx server errors, 6xxxx business errors. See `pkg/response/error_code.go`.
- **Money**: Ticket payment amounts use `int64` cents. Do not reintroduce floating-point money arithmetic.
- **Snowflake IDs**: All entity IDs are Snowflake int64, serialized as strings in JSON via `json:",string"` tag on `Base.ID`.
- **Soft delete**: All models embed `Base` with `gorm.DeletedAt`.
- **Redis keys**: Ticket quota, rush-sale, idempotency, rate-limit and lock keys use the current ticket namespace; inspect the service before adding a key.
- **Distributed locks**: `pkg/lock.WithLock` uses Redis SETNX + Lua script release (UUID-based ownership) to prevent deadlocks.
- **Publisher confirms**: RabbitMQ channel is set to confirm mode; `PublishPersistent` waits for broker ack with 5s timeout.
- **Config**: All config keys are env-overridable (e.g. `MYSQL_DSN`, `REDIS_PASSWORD`). The `.env` file is used by Docker Compose only. `backend/config/config.example.yaml` is the canonical reference; `backend/config/config.yaml` is gitignored (local dev).
- **Config sections**: `server`, `mysql`, `redis`, `rabbitmq`, `jwt`, `snowflake`, `log`, `ratelimit`, `cors`, `order_outbox`, `delayed_order`, `payment`, `ticket_qr` and telemetry settings.
- **GORM table naming**: `SingularTable: true` — table names match struct names exactly (no pluralization).
- **Admin seed**: Local startup may seed an initial admin account; use the configured bootstrap behavior and change credentials outside development.
- **Migrations**: Database changes are applied by `backend/migrations/runner.go` in filename order. Do not document startup automatic table changes as the production schema mechanism.
- **Known payment boundary**: Sandbox callback scheduling is in-memory. Restart recovery, real-channel querying, reconciliation, retryable refunds, and stronger callback/timeout concurrency handling are not complete.
