# AGENTS.md

This file is the fast-start guide for Codex agents working in this repository.
Keep it accurate and practical. Prefer facts from the current code over older
README text.

## How To Collaborate With The User

- Respond in Chinese unless the user asks otherwise.
- Do not blindly agree with the user. Separate facts, assumptions, suggestions,
  and uncertainty.
- Before larger changes, briefly explain the real problem, your assumptions,
  touched modules, chosen approach, risks, and validation plan.
- Keep changes small and reversible. Do not introduce frameworks, abstractions,
  dependencies, or broad refactors unless they are clearly necessary.
- Explain important design choices with project-specific code references.
- Do not treat "the code runs" as enough. Say how the user can verify behavior.
- If you find unrelated dirty worktree changes, do not revert them.

## Project Overview

**Gofun** — multi-organizer event ticketing platform (phase 1: no seat selection).

- Backend: Go, Gin, GORM, MySQL, Redis, RabbitMQ, Snowflake IDs, outbox pattern.
- Frontend: Vue 3, Vite, Element Plus, Vue Router, Axios.
- Core flows: normal ticket purchase + rush sale execute; Redis Lua quota pre-deduct → outbox → RabbitMQ async finalize.
- Docs: `docs/FUCHANG_PHASE1.md`, `docs/FUCHANG_INTERVIEW.md`, `interview-prep/00_CONTEXT_LOCK.md`.
- Monitoring: Prometheus `/metrics`, Grafana under `monitoring/`.

Legacy snack-commerce code has been removed; do not reference product/seckill/user-lock paths in new answers.

## Useful Commands

Run backend commands from `backend/`:

```powershell
go build -o whu-snack-go .
go run . -config ./config/config.yaml
go test ./... -count=1
go test ./models/ -run TestOrderStatus -v
go test ./service/ -run TestValidateCreateOrderInput -v
```

Run frontend commands from `frontend/`:

```powershell
npm.cmd install
npm.cmd run dev
npm.cmd run build
```

Full stack:

```powershell
Copy-Item deploy/env.example .env
docker compose up -d --build
docker compose logs -f backend
```

Load tests live in `tests/load/` and mutate real data for write scenarios. Use a
dedicated database, Redis, and queue before running:

```powershell
node tests/load/ticket_smoke.mjs
node tests/load/ticket_rush_spike.mjs
node tests/integration/rush_concurrency.mjs
```

See `tests/load/results/capacity-buckets-compare-20260731.md` for capacity evidence.

Note: on some Windows shells, `npm.ps1` may be blocked. Use `npm.cmd`. If `go`
is not in PATH, do not claim tests passed.

## Backend Structure

Main dependency flow:

```text
controller -> service -> repository -> models
             service -> Redis / RabbitMQ / DB transaction
main.go -> container.NewContainer -> services -> controllers -> routes
```

Important directories:

- `backend/main.go`: startup, dependency wiring, routes, background goroutines.
- `backend/container/`: builds DB, Redis, RabbitMQ, Snowflake, repositories.
  It also fills legacy `common.*` globals for middleware compatibility.
- `backend/common/`: legacy globals, JWT auth, admin auth, rate limiting,
  RabbitMQ helpers.
- `backend/controller/`: thin Gin handlers. They validate input, read user ID,
  call service, and wrap responses.
- `backend/service/`: business logic. Most important files are
  `order_service.go`, `order_consumer.go`, `order_timeout.go`,
  `seckill_service.go`, and `compensation_service.go`.
- `backend/repository/`: GORM data access interfaces and implementations.
- `backend/models/`: GORM models and order state machine.
- `backend/pkg/`: reusable packages such as `response`, `apperr`, `lock`,
  `logger`, `middleware`, `validator`, and `ws`.
- `backend/metrics/`: Prometheus metrics.

## Startup Flow

`backend/main.go` does the following:

1. Load config.
2. Initialize logger.
3. Build `container.Container`.
4. Initialize validator.
5. Build services and controllers.
6. Setup order timeout queue infrastructure.
7. Start WebSocket Hub.
8. Warm product stock into Redis.
9. Register routes and middleware.
10. Start background goroutines:
    - RabbitMQ order consumer.
    - RabbitMQ order timeout consumer.
    - HTTP server.
    - IP limiter cleanup every 30 minutes.
    - stock compensation every 5 minutes.
11. Wait for OS signal and shut down through context cancellation.

## Normal Order Flow

The normal order entry point is `OrderService.CreateOrder`.

```text
frontend api.createOrder()
-> POST /api/v1/orders
-> OrderController.CreateOrder
-> OrderService.CreateOrder
-> normalize item quantities
-> Redis user lock: lock:order:user:<userID>
-> optional idempotency SETNX: order:idempotency:<userID>:<key>
-> Redis Lua pre-deducts snack:stock:<productID>
-> publish OrderMessage to RabbitMQ
-> return before MySQL order is created
```

The real order row is created asynchronously:

```text
OrderConsumerService workers
-> RabbitMQ delivery channel
-> OrderService.ProcessOrderTask
-> DB transaction
-> validate products
-> decrement MySQL product stock
-> create Order(status=Pending) and OrderItems
-> increment sales_count
-> create SeckillOrder if this came from seckill
-> publish delayed timeout message
-> push WebSocket "created" event
```

Important consequence:

- "create order API returned success" does not guarantee the order is already
  queryable in MySQL.
- "order created" still means `Pending`, not paid.

## Payment, Cancellation, And Order States

Current order states in `models/model.go`:

```text
Pending(1)
Paid(2)
Completed(3)
Cancelled(5)
```

Allowed transitions:

```text
Pending -> Paid
Pending -> Cancelled
Paid -> Completed
Paid -> Cancelled
Completed -> Cancelled
Cancelled -> terminal
```

The project uses a pay-when-paid model:

- Create order: reserve stock only. Do not deduct user balance.
- Pay order: `Pending -> Paid`, deduct balance atomically in the same DB
  transaction.
- Cancel pending order: restore stock only.
- Cancel paid/completed order: restore stock and refund balance.
- `HasBeenPaid()` is the source of truth for refund decisions.

## Order Timeout Flow

`OrderTimeoutService` uses RabbitMQ delayed behavior through a delay queue and
dead-letter routing:

```text
ProcessOrderTask success
-> PublishDelayedOrderTimeout(orderID, userID)
-> message waits in order_delay_queue with per-message TTL
-> expired message routes to order_timeout_queue
-> timeout worker checks order
-> if still Pending, conditionally update to Cancelled
-> restore MySQL stock
-> after transaction commit, restore Redis stock
-> push WebSocket timeout_cancelled event
```

The conditional update avoids double handling when payment and timeout race.

## Seckill Flow

Seckill service has a special Redis front door, then reuses the normal order
consumer flow.

```text
admin warmup
-> Redis seckill:stock:<activityID>
-> user requests token
-> Redis seckill:token:<activityID>:<userID>, TTL 60s
-> user executes seckill
-> Redis Lua checks token, stock, per-user limit
-> deduct seckill stock and increment user count
-> publish OrderMessage with SeckillActivityID and SeckillPrice
-> normal RabbitMQ consumer creates the order
```

Important Redis keys:

- `snack:stock:<productID>`: normal product stock.
- `seckill:stock:<activityID>`: seckill stock.
- `seckill:user_count:<activityID>`: per-user seckill purchase count hash.
- `seckill:token:<activityID>:<userID>`: one-shot seckill token.
- `lock:*`: Redis distributed locks.

## Goroutines And Channels

The project uses explicit goroutines and channels in business code:

- `main.go` starts WebSocket Hub, order consumer, timeout consumer, HTTP server,
  IP limiter cleanup, and stock compensation goroutines.
- `service/order_consumer.go` starts multiple worker goroutines based on
  `order_consumer.worker_count`.
- `service/order_timeout.go` starts timeout worker goroutines.
- `pkg/ws/hub.go` defines custom channels:
  - `register chan *Client`
  - `unregister chan *Client`
  - `send chan []byte`
- `main.go` uses `make(chan os.Signal, 1)` for graceful shutdown.
- RabbitMQ `Consume` returns delivery channels consumed with `select`.

## Frontend Structure

Important files:

- `frontend/src/api/index.js`: Axios instance, auth header, token refresh,
  idempotency key generation, API methods.
- `frontend/src/router/index.js`: route definitions and auth/admin guards.
- `frontend/src/stores/cart.js`: local reactive cart composable. It is not a
  server-side cart and is not Pinia.
- `frontend/src/stores/orderSocket.js`: WebSocket client for order status
  events.
- `frontend/src/layouts/LayoutMain.vue`: shell layout, cart drawer, checkout,
  user refresh, socket lifecycle.
- `frontend/src/views/SeckillDetail.vue`: token request and seckill execution.
- `frontend/src/views/OrderDetail.vue`: pay, cancel, refund, confirm actions.

IDs from the Go API are serialized as strings by `json:",string"`. The frontend
often converts IDs to numbers for cart operations.

## API And Response Conventions

- Base API path: `/api/v1`.
- Frontend dev proxy maps `/api` to `http://127.0.0.1:8080`.
- JSON response shape is in `pkg/response/response.go`:

```json
{
  "code": 0,
  "msg": "ok",
  "data": {},
  "request_id": "..."
}
```

- Business codes are in `pkg/response/error_code.go`:
  - `4xxxx`: client/auth/resource errors.
  - `5xxxx`: server/DB/Redis/MQ errors.
  - `6xxxx`: business errors.

## Data And Consistency Conventions

- MySQL is the final source of truth for orders and product stock.
- Redis is a front-side inventory and concurrency guard.
- Stock compensation only pulls Redis down when Redis stock is greater than
  MySQL stock, or recreates missing Redis stock.
- Publisher confirms are enabled for normal order publishing.
- `OrderMessage.OrderID` is generated before publishing. Consumer checks for an
  existing order ID to make processing idempotent.
- Distributed lock implementation is in `pkg/lock/redis_lock.go`: `SETNX` with
  UUID value and Lua release.
- Money is currently `float64` despite GORM decimal tags. This is a known risk;
  prefer `int64` cents or a decimal library for future serious money changes.
- GORM uses `SingularTable: true`.
- Models embed `Base` with soft delete via `gorm.DeletedAt`.
- Snowflake IDs are `int64` and JSON-encoded as strings.

## Config Notes

- Canonical sample config: `backend/config/config.example.yaml`.
- Local config: `backend/config/config.yaml`.
- Docker `.env` is copied from `deploy/env.example`.
- Important config sections: `server`, `mysql`, `redis`, `rabbitmq`, `jwt`,
  `snowflake`, `log`, `ratelimit`, `order_consumer`, `cors`, `delayed_order`.
- On startup, if no admin exists, the app creates `admin` / `admin123`.

## Testing And Validation Strategy

For narrow backend changes:

```powershell
cd backend
go test ./models/ -count=1
go test ./service/ -count=1
go test ./pkg/... -count=1
```

For broader backend changes:

```powershell
cd backend
go test ./... -count=1
```

For frontend changes:

```powershell
cd frontend
npm.cmd run build
```

For order/seckill changes, do not rely only on unit tests. Validate the runtime
chain when possible:

```text
config -> DB/Redis/RabbitMQ connections -> HTTP route -> Redis key changes
-> RabbitMQ message -> MySQL order row -> WebSocket/client refresh
```

## Common Pitfalls

- Do not assume order creation is synchronous.
- Do not deduct balance during order creation; payment owns balance deduction.
- Do not refund pending orders; pending orders were never paid.
- Do not bypass `CanTransitionTo()` and `HasBeenPaid()` for order status logic.
- Do not update product stock in MySQL without considering Redis stock refresh.
- Do not add a server cart unless explicitly requested; the current cart is
  frontend-local.
- Do not introduce a second dependency wiring style. New code should prefer
  `container.Container` over new `common` globals.
- Do not claim README descriptions are current when code disagrees.
- Be careful with Chinese text encoding in PowerShell output; `rg` may show
  correct text even when `Get-Content` displays mojibake.

## Files Worth Reading First

For most future work, start with these files:

```text
backend/main.go
backend/container/init.go
backend/models/model.go
backend/service/order_service.go
backend/service/order_consumer.go
backend/service/order_timeout.go
backend/service/seckill_service.go
backend/pkg/ws/hub.go
frontend/src/api/index.js
frontend/src/layouts/LayoutMain.vue
frontend/src/stores/cart.js
frontend/src/stores/orderSocket.js
```
