# 赴场票务压测计划

> 旧零食脚本（`smoke.mjs` / `seckill_spike.mjs` 等）已废弃。当前以 `ticket_*.mjs` 为准。

This folder contains local load-test scripts that use Node.js built-in `fetch`.
They are designed for repeatable backend pressure tests without installing k6/wrk.

## Current Scope

Run these first:

1. `ticket_smoke.mjs` — 环境校验 + 浏览活动 + 普通购票一条链路。
2. `ticket_rush_spike.mjs` — 单热点限时开售 execute 压测。
3. `ticket_rush_staircase.mjs` / `ticket_rush_wave_sweep.mjs` — 阶梯/ WAVE 扫档找拐点。
4. `tests/integration/rush_concurrency.mjs` — 幂等/限购/不超卖集成验收。
5. **`tests/load/k6/`** — k6 入口吞吐/p99（见 `tests/load/k6/README.md`）。
6. 容量对比报告：`tests/load/results/capacity-buckets-compare-20260731.md`。

Long soak tests are intentionally not part of the default flow. A soak test means
running a stable workload for hours to find memory leaks, connection leaks, queue
backlog, or latency drift. It is useful, but it is expensive and should be run
after the short scenarios are stable.

## Pre-Test Environment

Use a dedicated load-test database/Redis DB/queue when possible. If you run
against local dev data, write scenarios will create orders, reduce stock, and
change balances.

For raw capacity tests, raise rate limits in `backend/config/config.yaml`:

```yaml
ratelimit:
  global_rate: 100000
  global_burst: 100000
  ip_rate: 100000
  ip_burst: 100000
```

Restart the backend after changing config.

For production-policy tests, restore the real limits and run a separate pass.

## Common Parameters

All scripts accept these environment variables:

```powershell
$env:BASE_URL='http://127.0.0.1:8080/api/v1'
$env:CONCURRENCY='100'
$env:DURATION_SECONDS='60'
$env:THINK_MS='0'
$env:ADMIN_USERNAME='admin'
$env:ADMIN_PASSWORD='admin123'
$env:LOAD_USER_PREFIX='load_user_'
$env:LOAD_PASSWORD='123456'
$env:DORM_ID='1'
```

`THINK_MS` adds a delay after each worker action. Use it for realistic user
pacing. Set it to `0` for raw throughput.

## Scenarios

### 1. Smoke

Runs login/register, product list/detail, categories, user info, create order,
and order list.

```powershell
node tests\load\smoke.mjs
```

Expected result:

- No network errors.
- Mostly `200`.
- One order may be created.

### 2. Read Baseline

Weighted read-only traffic:

- Product list: 60%
- Product detail: 25%
- Categories: 10%
- Seckill list: 5%

```powershell
$env:CONCURRENCY='100'
$env:DURATION_SECONDS='60'
$env:THINK_MS='0'
node tests\load\read_baseline.mjs
```

Use this to establish backend read capacity without order writes.

### 3. Shopping Mix

Weighted user traffic:

- Browse/list/detail: 65%
- Create order: 15%
- Order list: 10%
- User info: 5%
- Seckill list: 5%

```powershell
$env:CONCURRENCY='50'
$env:DURATION_SECONDS='300'
$env:THINK_MS='300'
node tests\load\shopping_mix.mjs
```

This is closer to real product usage. It writes orders.

### 4. Order Write

Focuses on `POST /orders`.

```powershell
$env:CONCURRENCY='50'
$env:DURATION_SECONDS='120'
$env:THINK_MS='100'
node tests\load\order_write.mjs
```

Post-check:

- RabbitMQ ready/unacked should return to zero.
- No negative product stock.
- Redis stock should converge with MySQL stock after consumers drain.
- User balances should not increase unexpectedly.

### 5. Seckill Spike

Requires at least one active and warmed-up seckill activity.

```powershell
$env:CONCURRENCY='500'
$env:DURATION_SECONDS='60'
$env:THINK_MS='0'
node tests\load\seckill_spike.mjs
```

Post-check:

- Successful seckill orders must be less than or equal to activity stock.
- One user must not exceed `limit_per_user`.
- `seckill:stock:<activity_id>` plus successful purchases should match initial stock.
- RabbitMQ backlog should drain.

## Metrics To Record

Each script prints JSON with:

- total requests
- requests per second
- failed requests and failure rate
- p50/p90/p95/p99/max latency
- request counts by label
- response status distribution
- top error messages

During each run also watch:

- backend logs
- `/metrics`
- MySQL connections and slow queries
- Redis ops/sec and memory
- RabbitMQ ready/unacked messages
- CPU, memory, and TIME_WAIT count

## Suggested Execution Order

1. `smoke.mjs`
2. `read_baseline.mjs` at 20, 100, 300 concurrency
3. `shopping_mix.mjs` at realistic pacing
4. `order_write.mjs` with conservative concurrency
5. `seckill_spike.mjs` after preparing a dedicated seckill activity
6. Restore real rate limits and repeat a smaller pass for limit-policy validation
7. Soak test later, only after all short tests are stable
