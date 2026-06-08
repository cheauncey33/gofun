# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

WHU Snack GO is a campus snack ordering system with a Go backend (Gin + GORM) and Vue 3 frontend (Element Plus + Vite). It features normal product ordering and flash-sale (seckill) activities with Redis-based inventory pre-deduction and RabbitMQ-based async order processing.

## Development Commands

### Backend (Go 1.25)

```bash
# Build
cd backend && go build -o whu-snack-go .

# Run (requires running MySQL, Redis, RabbitMQ)
go run . -config ./config/config.yaml

# Run all tests
go test ./... -count=1

# Run a single test file
go test ./tests/unit/ -run TestOrderStateMachine -v

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
node tests/load/smoke.mjs          # validate env + one full user path (run first)
node tests/load/read_baseline.mjs  # read-only capacity
node tests/load/shopping_mix.mjs   # normal browse + occasional order
node tests/load/order_write.mjs    # Redis pre-deduct → MQ → MySQL write path
node tests/load/seckill_spike.mjs  # seckill token + execute paths
```

Write scenarios mutate real data (create orders, reduce stock, change balances) — use a dedicated DB/Redis/queue. For raw-capacity runs, raise `ratelimit.*` in `config.yaml` and restart. See `tests/load/LOAD_TEST_PLAN.md`.

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
  → Redis Lua script pre-deducts stock (snack:stock:<productID>)
  → Publish OrderMessage to RabbitMQ order_queue (persistent, publisher-confirms)
  → Return immediately (user sees "order processing")

Consumer worker (order_consumer.go):
  → ProcessOrderTask: DB transaction validates products, deducts MySQL stock,
    deducts user balance, creates Order + OrderItems
  → On non-retryable error: rollback Redis reserved stock, Ack
  → On retryable error: publish to retry queue (x-retry-count header)
  → After max retries: publish to DLQ via DLX
  → On success: publish delayed timeout message for auto-cancel
```

### Order timeout (delayed queue)

```
OrderTimeoutService publishes to order_delay_queue with TTL (default 15min).
When TTL expires, message routes to order_timeout_queue via DLX.
Timeout worker: if order still pending → cancel, restore stock + balance.
```

### Seckill (flash sale)

```
1. Admin calls warmup → loads activity stock into Redis (seckill:stock:<id>)
2. User requests token → seckill:token:<activityID>:<userID> stored in Redis (60s TTL)
3. User executes seckill: Lua script atomically validates token, checks stock,
   checks per-user limit, deducts → publishes OrderMessage to MQ
4. Same consumer flow as normal orders
```

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

### Frontend routing

Simple Vue Router with route guards: unauth users redirected to `/login`, non-admin users blocked from `/admin/*` routes. Element Plus UI components throughout.

## Key Conventions

- **Error codes**: 5-digit business codes: 4xxxx client errors, 5xxxx server errors, 6xxxx business errors. See `pkg/response/error_code.go`.
- **Money**: `float64` in models (known issue — TODO comments recommend migrating to int64 cents or `shopspring/decimal`).
- **Snowflake IDs**: All entity IDs are Snowflake int64, serialized as strings in JSON via `json:",string"` tag on `Base.ID`.
- **Soft delete**: All models embed `Base` with `gorm.DeletedAt`.
- **Redis keys**: `snack:stock:<productID>` for normal inventory, `seckill:stock:<activityID>` for seckill, `lock:*` for distributed locks.
- **Distributed locks**: `pkg/lock.WithLock` uses Redis SETNX + Lua script release (UUID-based ownership) to prevent deadlocks.
- **Publisher confirms**: RabbitMQ channel is set to confirm mode; `PublishPersistent` waits for broker ack with 5s timeout.
- **Config**: All config keys are env-overridable (e.g. `MYSQL_DSN`, `REDIS_PASSWORD`). The `.env` file is used by Docker Compose only.
- **GORM table naming**: `SingularTable: true` — table names match struct names exactly (no pluralization).
