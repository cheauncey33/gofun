# Gofun · 高并发校园活动票务平台

> 去想去的地方，见想见的人。

Gofun 是一个用 Go 构建的多主办方活动票务平台，核心难点是 **限时开售（抢票）瞬间的高并发写入**：
开售峰值下保证不超卖、不重复下单、入口低延迟。本项目以「可量化压测 + 可观测的一致性设计」为目标，
而非单纯堆功能。

## 一句话技术主张

开售瞬间高并发抢票：**Redis Lua 入口预扣 → 事务性 Outbox → RabbitMQ 异步落单 → MySQL 库存分桶（8 桶打散行锁）→ 支付/超时关单 → 电子票核销**，全链路幂等 + 补偿对账，压测无超卖。

## 关键技术亮点

| 主题 | 做法 | 证据 |
|------|------|------|
| **入口抢票** | Redis Lua 原子校验「活动票额 + 底层票档 + 个人限购」，预扣后异步落单 | `service/rush_sale_service.go` |
| **库存热点优化** | MySQL 票档按 `userID % N` 分桶，父表退出热路径 | InnoDB `row_lock_waits` **降约 80%**，MQ 追平 **23s→11s** |
| **入口吞吐** | k6 恒定 VU 阶梯压测 | 合格峰值（成功率≥99% 且 p99≤300ms）约 **1100~1450 req/s** |
| **写一致性** | 订单 + Outbox 同事务，publisher `SKIP LOCKED` 抢占投递，broker confirm | `service/ticket_order_outbox_publish.go` |
| **幂等** | `X-Idempotency-Key` + DB 唯一约束 + Redis 缓存 + 消费按 order_id 查重 | 重复提交/重试/重投递均不重复落单 |
| **超时关单** | RabbitMQ 延时队列 + DB 扫描兜底，条件 UPDATE 防支付/超时竞态 | `service/ticket_order_timeout.go` |

> 完整数据与复盘见 **[性能与压测报告](docs/PERFORMANCE.md)**、**[一致性设计](docs/CONSISTENCY.md)**。

## 核心链路

```text
用户抢票
  → 写限流 + 幂等键（X-Idempotency-Key）
  → Redis Lua 原子预扣（活动票额 + 票档库存 + 个人限购，分桶选桶）
  → MySQL 事务：写订单(queued) + Outbox 行
  → 返回「排队中」，用户轮询/WebSocket 拿结果
  → Outbox publisher 异步投递 RabbitMQ（confirm）
  → Consumer 事务确认：MySQL 分桶扣减、订单 queued→pending_payment
  → 支付（余额）→ 出票 / 超时关单 → 释放库存
  → 补偿对账：周期性对齐 Redis 与 MySQL 票额（只下调/补缺）
```

## 技术栈

- **后端**：Go 1.25、Gin、GORM、MySQL 8、Redis 7、RabbitMQ 3、Snowflake ID
- **前端**：Vue 3、Vite、Element Plus、Vue Router、Axios
- **可观测性**：Prometheus `/metrics`、Grafana、OpenTelemetry trace、pprof
- **压测**：k6（入口峰值）+ Node 脚本（业务正确性/MQ 追平）

## 本地运行

后端：

```powershell
cd backend
go run . -config ./config/config.yaml
```

前端：

```powershell
cd frontend
npm.cmd install
npm.cmd run dev
```

验证：

```powershell
cd backend
go test ./... -count=1

cd ../frontend
npm.cmd run build
```

## 压测复现

```powershell
# 起压测栈（capacity 单实例 + 8 桶）
docker compose -p whu-snack-go-capacity `
  -f tests/integration/docker-compose.ticketing.yml `
  -f tests/load/docker-compose.capacity.yml up -d

# 准备夹具并跑 k6 峰值扫档
node tests/load/k6/prepare_rush_fixture.mjs
node tests/load/k6/run_peak_sweep.mjs
```

详见 [tests/load/k6/README.md](tests/load/k6/README.md)。

## 重要一致性约定

- MySQL 是票务订单和票额的最终事实来源；Redis 是前置并发闸门。
- Redis 键使用 `fuchang:ticket:*` 与 `fuchang:rush:*` 命名空间；RabbitMQ 队列用 `fuchang.order.*`。
- 上述 `fuchang` 仅是存量 Redis/MQ 的兼容命名空间，不是产品名称；产品对外统一为 **Gofun**。迁移命名空间前需先完成旧键、队列和历史消息迁移。
- 票务金额一律 `int64` 分。
- `queued` 表示订单凭据已创建、消费者尚未完成 MySQL 票额确认，**不是支付成功**。

## 文档导航

- [性能与压测报告](docs/PERFORMANCE.md) — 峰值数据、分桶 v1→v2 复盘
- [一致性设计](docs/CONSISTENCY.md) — 预扣 / Outbox / 幂等 / 补偿
- [第一阶段范围](docs/FUCHANG_PHASE1.md) · [主办方闭环](docs/FUCHANG_ORGANIZER_CLOSURE.md) · [电子票与核销](docs/FUCHANG_ADMISSION_TICKET.md)
