# AGENTS.md

This file is the fast-start guide for Codex agents working in this repository.
Keep it accurate and practical. Prefer facts from the current code over copied
or outdated descriptions.

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
- Docs: `docs/FUCHANG_PHASE1.md`, `docs/CONSISTENCY.md`, `docs/FUCHANG_PAYMENT_SANDBOX.md`, `interview-prep/00_CONTEXT_LOCK.md`.
- Monitoring: Prometheus `/metrics`, Grafana under `monitoring/`.

新回答只引用当前票务路径和实现。

## Useful Commands

Run backend commands from `backend/`:

```powershell
go build -o gofun-server .
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
  It also fills compatibility `common.*` globals for middleware.
- `backend/common/`: compatibility globals, JWT auth, admin auth, rate limiting,
  RabbitMQ helpers.
- `backend/controller/`: thin Gin handlers. They validate input, read user ID,
  call service, and wrap responses.
- `backend/service/`: business logic. Most important files are
  `ticket_catalog_service.go`, `ticket_order_service.go`,
  `rush_sale_service.go`, `ticket_order_timeout.go`,
  `ticket_order_outbox_publish.go`, `payment_gateway.go`,
  `ticket_verification_service.go`, and `ticket_compensation_service.go`.
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
8. Warm ticket-tier quota into Redis when the runtime configuration enables it.
9. Register routes and middleware.
10. Start background goroutines:
    - RabbitMQ ticket-order consumer.
    - RabbitMQ Outbox publisher.
    - RabbitMQ payment-timeout consumer.
    - HTTP server.
    - IP limiter cleanup every 30 minutes.
    - ticket-quota compensation on its configured interval.
11. Wait for OS signal and shut down through context cancellation.

## Ticket Order Flow

普通购票和抢票最终都进入票务订单异步链路。

```text
frontend API
-> ticket-order or rush-sale controller
-> validate event, tier, quantity, limit and idempotency key
-> Redis Lua pre-deducts ticket-tier quota
-> writes ticket-order Outbox row
-> publisher-confirmed RabbitMQ delivery
-> ticket-order consumer creates TicketOrder(pending_payment) and items
-> publishes payment-timeout message
-> pushes queued / pending_payment WebSocket events
```

The real order row is created asynchronously:

```text
TicketOrder consumer
-> RabbitMQ delivery channel
-> DB transaction creates TicketOrder(pending_payment) and items
-> publishes delayed payment-timeout message
-> pushes order status WebSocket event
```

Important consequence:

- "create order API returned success" does not guarantee the order is already
  queryable in MySQL.
- `pending_payment` means inventory was reserved and payment is still
  pending; it is not payment success.
- MySQL is the final source of truth; Redis is the front-door quota guard.

## Payment, Cancellation, And Order States

Current ticket-order states are `queued`, `pending_payment`,
`paid`, `cancelled` and `refunded`. Payment
transactions are `pending`, `paid`, `failed` or
`refunded`.

```text
```

Allowed transitions:

```text
```

支付成功才允许订单进入 `paid` 并签发电子票；支付失败、超时和
取消不会签发电子票。状态变更由 service 校验并在数据库事务中条件更新。
取消待支付订单恢复预扣库存；退款订单恢复库存并撤销或标记对应电子票。
票务支付不读取或扣减用户历史余额字段。

## Payment Timeout Flow

`TicketOrderService` publishes a delayed payment-timeout message through
RabbitMQ delay/dead-letter routing:

```text
ticket order created
-> payment-timeout message waits with per-message TTL
-> expired message reaches payment-timeout consumer
-> consumer checks order and payment state
-> if still pending_payment, conditionally update to cancelled
-> restore ticket quota
-> push timeout_cancelled event
```

The conditional update avoids double handling when payment and timeout race.

## Rush Sale Flow

Rush sale has a Redis front door, then reuses the ticket-order Outbox and
RabbitMQ consumer flow.

```text
organizer prepares event and tier quota
-> user opens the rush-sale endpoint
-> service validates time window, tier, quota and per-user limit
-> Redis Lua atomically reserves quota and records idempotency
-> writes ticket-order Outbox row
-> RabbitMQ consumer creates a pending_payment ticket order
```

Redis keys are ticket quota, rush-sale, idempotency, rate-limit and lock keys.
Use the key constants and service implementation as the source of truth.

## Goroutines And Channels

The project uses explicit goroutines and channels in business code:

- `main.go` starts WebSocket Hub, ticket-order consumer, Outbox publisher,
  payment-timeout consumer, HTTP server, IP limiter cleanup and compensation.
- `service/ticket_order_consumer.go` starts multiple workers from
  `order_consumer.worker_count`.
- `service/ticket_order_timeout.go` starts payment-timeout workers.
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
- `frontend/src/views/EventDetail.vue`, `Checkout.vue`, and `Cashier.vue`: event,
  ticket selection, checkout and sandbox payment.
- `frontend/src/views/OrganizerConsole.vue`: organizer overview and management.
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

- MySQL is the final source of truth for ticket orders, payments, tickets and quota.
- Redis is a front-side quota and concurrency guard.
- Quota compensation repairs missing or over-reserved Redis values from MySQL.
- Publisher confirms are enabled for ticket-order Outbox publishing.
- Outbox/order identifiers are generated before publishing; consumer checks existing
  rows so retries remain idempotent.
- Distributed lock implementation is in `pkg/lock/redis_lock.go`: `SETNX` with
  UUID value and Lua release.
- Payment amounts use `int64` cents.
- GORM uses `SingularTable: true`.
- Models embed `Base` with soft delete via `gorm.DeletedAt`.
- Snowflake IDs are `int64` and JSON-encoded as strings.

## Config Notes

- Canonical sample config: `backend/config/config.example.yaml`.
- Local config: `backend/config/config.yaml`.
- Docker `.env` is copied from `deploy/env.example`.
- Important config sections: `server`, `mysql`, `redis`, `rabbitmq`, `jwt`,
  `snowflake`, `log`, `ratelimit`, `order_consumer`,
  `order_outbox`, `delayed_order`, `payment`, `ticket_qr` and `cors`.
- Database changes are applied by versioned migrations in `backend/migrations/runner.go`.

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

For order/rush-sale/payment changes, do not rely only on unit tests. Validate the runtime
chain when possible:

```text
config -> DB/Redis/RabbitMQ connections -> HTTP route -> Redis key changes
-> RabbitMQ message -> MySQL order row -> WebSocket/client refresh
```

## Common Pitfalls

- Do not assume order creation is synchronous.
- Do not describe `pending_payment` as payment success.
- Do not issue tickets before a verified successful payment callback.
- Do not refund a payment or revoke a ticket without checking payment and ticket status.
- Do not update MySQL quota without considering Redis reconciliation.
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
backend/models/ticket_order.go
backend/service/ticket_order_service.go
backend/service/ticket_order_consumer.go
backend/service/ticket_order_timeout.go
backend/service/payment_gateway.go
backend/service/ticket_verification_service.go
backend/pkg/ws/hub.go
frontend/src/api/index.js
frontend/src/layouts/LayoutMain.vue
frontend/src/stores/cart.js
frontend/src/stores/orderSocket.js
```
