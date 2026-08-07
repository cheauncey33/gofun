# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**赴场（Fuchang）** — event ticketing platform (Go + Vue 3). Normal purchase and rush-sale execute use Redis quota pre-deduct, transactional outbox, and RabbitMQ async order finalization. See `docs/FUCHANG_PHASE1.md` for scope; `interview-prep/00_CONTEXT_LOCK.md` for interview facts.

> Legacy snack-commerce architecture below is **outdated**; prefer `backend/main.go` and ticketing services when editing docs.

## Development Commands

### Backend (Go 1.25)

All commands run from `backend/`:

```bash
# Build
cd backend && go build -o whu-snack-go .

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
cd frontend && npm install && npm run dev      # dev server on :5173
cd frontend && npm run build                    # production build → dist/
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

`monitoring/docker-compose.monitoring.yml` runs Prometheus + Grafana separately from the app stack, scraping the backend's `/metrics` endpoint. Grafana dashboard: `monitoring/grafana/dashboards/whu_snack_overview.json`.

## Architecture

### Layered backend

```
controller → service → repository → models (GORM)
                 ↓
          container (DI: DB, Redis, MQ, Snowflake, repos)
```

- **`container/`** — DI container that wires DB, Redis, RabbitMQ, Snowflake, and repositories. Also sets legacy `common` package globals for middleware compatibility.
- **`common/`** — Global singletons (`common.DB`, `common.RDB`, `common.MQChannel`, `common.Node`) plus auth middleware, JWT, rate limiting, and RabbitMQ helpers. Partially legacy — new code prefers `container.Container`.
- **`controller/`** — Gin handlers. Thin: validate input, call service, return response.
- **`service/`** — Business logic. Services receive `*container.Container` or individual dependencies.
- **`repository/`** — Interface-based data access over GORM. Interfaces (e.g. `OrderRepository`, `ProductRepository`) are defined alongside implementations.
- **`models/`** — GORM model structs with soft-delete via `gorm.DeletedAt`. Includes order state machine (`CanTransitionTo`).
- **`pkg/`** — Reusable packages: `response` (JSON response helpers + error codes), `apperr` (typed app errors), `lock` (Redis distributed lock), `logger` (zap + lumberjack), `middleware` (request ID), `validator` (custom validations).
- **`metrics/`** — Prometheus metrics exposed at `/metrics`, with middleware tracking HTTP requests, order events, seckill outcomes, and MQ messages.
- **`config/`** — Viper-based config with YAML file + env var overrides. `GlobalConfig` singleton.

### Order flow (async)

```
User request → controller → service.CreateOrder
  → Idempotency check: if idempotency_key present, SETNX <idempotency_key> (10min TTL)
    prevents duplicate submissions; released on failure or after order completes
  → User-level distributed lock (lock:order:user:<userID>) prevents concurrent orders
  → Redis Lua script pre-deducts stock (snack:stock:<productID>)
  → Publish OrderMessage to RabbitMQ order_queue (persistent, publisher-confirms)
  → Return immediately (user sees "order processing")

Consumer worker (order_consumer.go):
  → ProcessOrderTask: DB transaction validates products, deducts MySQL stock,
    creates Order (status=Pending) + OrderItems — balance is NOT deducted here
  → On non-retryable error: rollback Redis reserved stock + release idempotency key, Ack
  → On retryable error: publish to retry queue (x-retry-count header)
  → After max retries: publish to DLQ via DLX
  → On success: publish delayed timeout message (auto-cancel if not paid in time)

Payment (separate step): user calls PayOrder → DB transaction conditionally
  updates Pending→Paid AND deducts balance atomically (RowsAffected==0 rejects
  concurrent pays/timeouts). This "pay-when-paid" model means cancel of a
  Pending order only restores stock (no refund needed).
```

### Order state machine

7 states: `Pending(1) → Paid(2) → Delivering(3) → Delivered(4)`. Terminal: `Cancelled(5)`, `Refunded(7)`. `Refunding(6)` is an intermediate state before `Refunded`.

Transitions are defined in `models/model.go:orderTransitionMap`. `HasBeenPaid()` determines whether cancel requires balance refund (Paid/Delivering/Delivered/Refunding/Refunded = was paid) or just stock restoration (Pending/Cancelled = never paid).

### Background goroutines (started in main.go)

| Goroutine | Interval | Purpose |
|-----------|----------|---------|
| Order consumer | event-driven (RabbitMQ) | Processes order_queue + retry_queue messages |
| Timeout consumer | event-driven (RabbitMQ) | Listens on order_timeout_queue for expired orders |
| Stock compensation | 5 min | Compares Redis vs MySQL stock, resets Redis if oversold |
| IP limiter cleanup | 30 min | Removes stale per-IP rate limiters |

### Order timeout (delayed queue)

```
OrderTimeoutService publishes to order_delay_queue with TTL (default 15min).
When TTL expires, message routes to order_timeout_queue via DLX.
Timeout worker: if order still pending → cancel, restore stock + balance.
```

### Seckill (flash sale)

```
1. Admin calls warmup → loads activity stock into Redis (seckill:stock:<id>)
   with TTL = time until activity end
2. User requests token → HMAC-SHA256(activityID:userID:timestamp) stored in
   Redis as seckill:token:<activityID>:<userID> with 60s TTL
3. User executes seckill: Lua script atomically:
   a. Validates token (GET + DEL to one-shot the token)
   b. Checks stock (seckill:stock:<id>)
   c. Checks per-user limit (seckill:user_count:<id> hash)
   d. Deducts stock + increments user count
   e. Extends TTL on stock/user keys
   → Publishes OrderMessage to MQ (same consumer flow as normal orders)
```

Seckill Redis keys auto-expire when the activity ends (TTL = endTime - now).
Admin operations (update/delete) refresh or clear Redis keys accordingly.

### Stock compensation

Runs every 5 minutes: scans all products, compares Redis stock vs MySQL stock. If Redis > MySQL (oversell risk), resets Redis to MySQL value.

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
- **Money**: `float64` in models (known issue — TODO comments recommend migrating to int64 cents or `shopspring/decimal`).
- **Snowflake IDs**: All entity IDs are Snowflake int64, serialized as strings in JSON via `json:",string"` tag on `Base.ID`.
- **Soft delete**: All models embed `Base` with `gorm.DeletedAt`.
- **Redis keys**: `snack:stock:<productID>` for normal inventory, `seckill:stock:<activityID>` for seckill, `lock:*` for distributed locks.
- **Distributed locks**: `pkg/lock.WithLock` uses Redis SETNX + Lua script release (UUID-based ownership) to prevent deadlocks.
- **Publisher confirms**: RabbitMQ channel is set to confirm mode; `PublishPersistent` waits for broker ack with 5s timeout.
- **Config**: All config keys are env-overridable (e.g. `MYSQL_DSN`, `REDIS_PASSWORD`). The `.env` file is used by Docker Compose only. `backend/config/config.example.yaml` is the canonical reference; `backend/config/config.yaml` is gitignored (local dev).
- **Config sections**: `server`, `mysql`, `redis`, `rabbitmq`, `jwt`, `snowflake`, `log`, `ratelimit`, `cors`, `order_consumer` (worker_count, prefetch_count, max_retries), `delayed_order` (timeout_minutes default 15, lock_timeout_sec default 10).
- **GORM table naming**: `SingularTable: true` — table names match struct names exactly (no pluralization).
- **Auto-admin seed**: On startup, if no admin user exists in DB, creates `admin`/`admin123` with role="admin" and balance=9999 (see `container/init.go`).
- **GORM AutoMigrate**: All models are auto-migrated on startup. The `uni_user_phone` index on `user` table is explicitly dropped (legacy workaround).
