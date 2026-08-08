# Gofun 票务 · 面试材料防偏离总控

> 仓库名仍为 `WHU_Snack_GO`，但 **当前运行代码是 Gofun 多主办方活动票务**，零食电商链路已下线。面试以本文件 + `docs/FUCHANG_INTERVIEW.md` 为准。

## 依据来源

- 入口与路由: `backend/main.go`
- 数据模型: `backend/models/model.go`, `backend/models/ticketing.go`, `backend/models/ticket_order.go`
- 核心服务: `backend/service/ticket_order_service.go`, `backend/service/ticket_order_hotpath.go`, `backend/service/rush_sale_service.go`, `backend/service/ticket_order_consumer.go`, `backend/service/ticket_compensation_service.go`
- 库存分桶: `backend/service/inventory_bucket.go`, `backend/service/inventory_bucket_ops.go`
- 基础设施: `backend/container/init.go`, `backend/common/ratelimit.go`, `backend/common/distributed_ratelimit.go`
- 面试话术样板: `docs/FUCHANG_INTERVIEW.md`
- 压测证据: `tests/load/results/capacity-buckets-compare-20260731.md`

## 项目真实边界

产品：**Gofun** — 多主办方活动票务平台（无选座第一阶段）。

核心能力：活动发现、普通购票、限时开售（rush）、支付沙箱回调、超时取消、电子票签发与核销。

真实技术栈：Go / Gin / GORM / MySQL / Redis / RabbitMQ / Snowflake / Prometheus / Vue 3。

## 已确认核心链路（当前代码）

### 普通购票 `POST /orders`

1. 校验票档可售、限购、观演人信息。
2. **幂等**：`X-Idempotency-Key` → Redis `fuchang:ticket:idem:{userId}:{key}` + MySQL `(user_id, idempotency_key)`。
3. **Redis Lua 预扣票额**（可选分桶：`fuchang:ticket:stock:{tierId}:{bucketNo}`）。
4. 同步写 MySQL：`ticket_order(status=queued)` + `ticket_order_outbox`（本地消息表/outbox）。
5. Outbox publisher 异步投递 RabbitMQ；接口返回 `queued`。
6. Consumer：`ProcessOrderTask` → 条件 UPDATE 扣 MySQL 票额 → `pending_payment`。
7. 支付 / 超时：支付单 + provider 回调幂等出票；超时条件取消并释放票额。

### 限时开售 `POST /rush-sales/:id/execute`

1. 到点直抢，**无 token 两步**；防刷靠登录 + 写限流 + 幂等键。
2. Redis Lua 扣 `fuchang:rush:stock:{campaignId}`（+ 分桶）+ 个人限购 hash。
3. 列表余量读可走 **go-cache 短 TTL（≈300ms）+ singleflight**；扣减只走 Redis。
4. 后续与普通票共用 outbox → MQ → consumer。

### MQ / 可靠性

- Durable queue + persistent message + **publisher confirm**。
- Consumer 多 worker + prefetch + 手动 Ack。
- 可重试 → retry queue；超限 → DLQ。
- 幂等：消费前查 `order_id`；MySQL 条件 UPDATE 兜底。

### 补偿

- `TicketCompensationService`：Redis 票额 > MySQL 可用量时 **只下调 Redis**。
- 启动时 `WarmTicketQuota`、`RecoverQueuedOrders` 重建 Redis 并重投 outbox。

### 限流

- 全局 + IP 令牌桶；写接口可选 **Redis 滑动窗口分布式限流**。

## 重要：当前系统 **没有** 用户级 Redis 分布式锁

旧零食代码曾有 `lock:order:user:{userId}`，**Gofun 购票/抢票未使用 `WithLock` 用户锁**。

并发控制靠：

| 手段 | 作用 |
|------|------|
| 幂等键 | 防重复提交（客户端 UUID + Redis + DB） |
| Redis Lua | 票额预扣原子性 |
| MySQL 条件 UPDATE | 消费端最终扣减、支付/超时竞态 |
| 写接口限流 | 防脚本刷接口 |
| 库存分桶 | 降低单行热点锁竞争 |

面试不要说「入口用用户锁」，应说 **「幂等键 + Lua 预扣 + 条件 UPDATE」**。

## 禁止幻觉清单

不能说成已实现：

- Redis Cluster / Redlock 生产级热点治理（分桶是已落地的拆 key）。
- 完整分布式事务 / Seata / TCC。
- 真实生产千万 QPS（只能说压测脚本 + 1500 并发证据）。
- 旧零食的商品列表缓存、秒杀 token、订单推送和用户锁均已移除；当前已按票务订单状态机重新实现 WebSocket 推送。

## 压测可引用数据（1500 并发单热点）

- 成功数 = 库存数，无超卖。
- 库存分桶 v2：MQ 追平约 23s → 11s；`row_lock_waits` 约降 80%。
- 详见 `tests/load/results/capacity-buckets-compare-20260731.md`。

## 回答口径

- 已实现：讲代码文件、关键 Redis key、失败分支。
- 未实现：先说「当前没做」，再讲扩展方案。
- 旧零食经验：可说「早期版本用过用户锁/秒杀 token，票务版改为幂等键 + 直抢 Lua」。

## 后续写作规则

- 每篇文档开头写依据来源。
- 结论指向文件或函数。
- 与代码冲突时 **以代码为准**，更新文档。
