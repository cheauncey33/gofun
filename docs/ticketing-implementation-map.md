# 票务源码实现地图（按当前代码核对，2026-08-30）

这不是设计建议，而是从 `backend/migrations` 和实际 Go 调用链反查出的当前实现。

## 先记住四个事实

1. **MySQL 是最终事实**：订单、订单明细、分桶库存、支付、Outbox、消费 Inbox 都在 MySQL。
2. **Redis 是抢购热路径的快速库存与在途预扣凭证**：它先扣，但不能独自代表订单成功。
3. **RabbitMQ 是至少一次投递**：重复消息由 MySQL `ticket_order_consumer_inbox` 去重。
4. **订单确认成功的定义**：`ticket_order` 与 `ticket_order_outbox` 在同一个 MySQL 事务提交；Redis reservation 随后才从 `pending` 变为 `committed`。

阅读入口：

- 普通下单：`backend/service/ticket_order_service.go` 的 `CreateOrder`
- 秒杀下单：`backend/service/rush_sale_service.go` 的 `Execute`
- Redis reservation 与恢复：`backend/service/ticket_stock_reservation.go`
- Outbox：`backend/service/ticket_order_hotpath.go`、`ticket_order_outbox_publish.go`
- MQ 消费：`backend/service/ticket_order_consumer.go`、`ticket_order_service.go` 的 `ProcessOrderTask`
- 支付和取消：`backend/service/ticket_order_service.go` 的 `PayOrder`、`HandlePaymentCallback`、`CancelOrder`

## 1. MySQL 表

`migrations/runner.go` 自动额外创建 `schema_migration(version, applied_at)`；其余业务表由 `001` 到 `020` 迁移创建或演进。下列 `Base` 均代表 `id, create_time, update_time, delete_time`，除非表明确不是 Base 模型。

### 下单热路径直接涉及的表

| 表 | 关键字段 / 唯一约束 | 谁写 / 谁读 |
| --- | --- | --- |
| `ticket_order` | `order_no`（唯一）；`user_id + idempotency_key`（唯一）；业务归属：`organizer_id,event_id,session_id,rush_sale_campaign_id,order_source`；状态：`status,payment_status`；金额：`total_amount_cents`；幂等防冲突：`request_hash`；库存对齐：`stock_bucket_no,rush_bucket_no`；支付窗口：`expires_at,paid_at,cancelled_at,cancel_reason`；联系人与实名快照字段；`funnel_visitor_key` | HTTP 下单插入 `queued`；MQ consumer 变更为 `pending_payment`；支付回调变更为 `paid`；取消/超时变更为 `cancelled`；查询接口读取。 |
| `ticket_order_item` | `order_id,ticket_tier_id,quantity,unit_price_cents` 和活动、场次、场地、票档快照 | 随订单同事务写；consumer、取消、出票读取。 |
| `ticket_order_attendee` | `order_id + sequence_no`（唯一）；姓名、证件掩码、哈希和 `identity_key` | 随订单写；实名限购、出票读取。 |
| `ticket_order_outbox` | `id=event_id`（主键）；`order_id,event_type,payload,status,attempts,last_error,published_at`；索引 `(status,create_time)` | 与订单同事务插入 `pending`；Outbox worker 认领、发布、标记结果。 |
| `ticket_order_consumer_inbox` | 主键 `(consumer_name,event_id)`，另有 `order_id` 索引 | Consumer 和库存扣减在同一事务插入；重复投递唯一键冲突即视为已处理。 |
| `ticket_stock_recovery_fence` | 主键 `order_id`；`owner=order/recovery` | 下单事务先插入 `order`；过期 reservation 恢复任务争抢 `recovery`，解决“订单尚未提交却被误回滚”的竞态。 |
| `ticket_tier` | `session_id,name,price_cents,total_quota,remaining_quota,sold_count,purchase_limit,status,version,waitlist_pending` | 管理端维护；计数票档 `status=on_sale/waitlist` 决定公开购票或候补模式；consumer 正式扣总库存或配合分桶。 |
| `ticket_tier_bucket` | 主键 `(tier_id,bucket_no)`；`remaining_quota,sold_count,version` | 目前配置已开启分桶：consumer 在事务中扣此表；取消/超时在事务中归还此表。 |
| `rush_sale_campaign` | `ticket_tier_id,rush_price_cents,total_quota,remaining_quota,per_user_limit,starts_at,ends_at,status` | 秒杀活动配置；consumer 扣活动库存（分桶开启时实际扣下一表）。 |
| `rush_campaign_bucket` | 主键 `(campaign_id,bucket_no)`；`remaining_quota,version` | 秒杀订单的 MySQL 分桶库存。 |
| `payment_transaction` | `payment_no`（唯一）；`order_id,waitlist_id,user_id,provider,provider_payment_id,amount_cents,status,expires_at,paid_at,refunded_at` | 点击支付时创建 `pending`；回调改为 `success/failed/closed/refunded`。 |
| `payment_callback` | `provider_event_id`（唯一）；`payment_no,provider,status,amount_cents,payload,processed_at` | 每个支付平台回调先插入；相同 `provider_event_id` 直接幂等返回。 |
| `admission_ticket` | `ticket_no`（唯一）；`order_item_id + sequence_no`（唯一）；`order_id`、归属信息、`status,issued_at,used_at,revoked_at` | 支付成功时签发 `valid`；取消退款时改 `revoked`；核销时改 `used`。 |
| `session_seat`（选座模式） | `(session_id,seat_id)` 唯一；`ticket_tier_id,order_id,status` | 选座下单同事务从 `available` 改 `held`；支付后改 `sold`；取消释放为 `available`。选座不走 Redis 计数库存和分桶。 |

### 其余业务表（也是当前完整库结构的一部分）

| 领域 | 表 | 主要用途 |
| --- | --- | --- |
| 用户/组织 | `user`、`organizer`、`organizer_member` | 账号、主办方、成员角色；`user.username`、`organizer.slug`、`(organizer_id,user_id)` 各自唯一。 |
| 活动目录 | `venue`、`event`、`event_session` | 场馆、活动、场次；订单用它们的 ID 并将展示信息快照到订单明细。 |
| 票档秒杀 | `rush_sale_campaign`、`ticket_tier`、`ticket_tier_bucket`、`rush_campaign_bucket` | 如上。 |
| 选座 | `seat_layout`、`seat`、`session_seat` | 厅图、座位模板、场次座位库存；`seat_layout.event_id` 和 `(layout_id,row_no,col_no)` 唯一。 |
| 实名/核销 | `user_attendee`、`ticket_order_attendee`、`admission_ticket`、`ticket_verification_record` | 常用观演人、订单快照、电子票、核销留痕。 |
| 候补 | `waitlist_entry`、`waitlist_attendee` | `waitlist_no` 与 `(user_id,idempotency_key)` 唯一；状态与订单不同，见后文。 |
| 评论 | `event_comment` | 活动评论与点赞计数。 |
| 主办方漏斗 | `funnel_daily`、`funnel_order_daily`、`funnel_visitor_daily` | 统计表，不参与下单一致性。 |

完整字段以迁移为准：`backend/migrations/001_ticketing_baseline.sql` 到 `020_order_identity.sql`。运行时不会依赖 GORM AutoMigrate 生成票务表。

## 2. Redis Key

### 与普通/秒杀下单直接有关的 Key

| Key 模板 | 类型、TTL | 值 / 字段 | 创建、删除和作用 |
| --- | --- | --- | --- |
| `fuchang:ticket:stock:{tierId}:{bucketNo}` | String；无 TTL | 分桶剩余可售张数 | 当前 `inventory.buckets_enabled=true` 时实际热库存。预热写入，Lua 原子 `DECRBY`，取消/超时回加。低库存会退化为 `bucketNo=0`。 |
| `fuchang:ticket:stock:{tierId}` | String；无 TTL | 未分桶模式剩余张数 | 兼容/测试模式。 |
| `fuchang:rush:stock:{campaignId}:{bucketNo}` | String；秒杀结束后约 1 小时 TTL | 秒杀活动分桶库存 | 秒杀 Lua 同时扣它和普通票档库存。 |
| `fuchang:rush:stock:{campaignId}` | String；同上 | 未分桶秒杀库存 | 兼容/测试模式。 |
| `fuchang:rush:user-count:{campaignId}:{userId}` | String；到活动结束后约 1 小时 | 该用户已抢数量 | Lua 先校验 `bought + quantity <= per_user_limit`，再原子加；回滚/取消时减。它不是请求幂等 key，而是**限购计数**。 |
| `fuchang:ticket:reservation:{orderId}` | Hash；新建时 `PERSIST`，提交后 24h | `order_id,user_id,tier_id,kind,campaign_id,quantity,stock_bucket_no,rush_bucket_no,idempotency_key,idempotency_map_key,request_hash,state,remaining,created_at_ms` | Redis Lua 新建 `pending` reservation；MySQL 订单事务成功后改 `committed` 并设 24h TTL；失败/孤儿恢复时删除。真正的库存补偿证据在这里。 |
| `fuchang:ticket:reservation:pending` | ZSET；长期存在 | member 为 reservation Hash key，score=`created_at_ms` | Lua 建 reservation 时 `ZADD`；提交/回滚 `ZREM`。恢复任务每 30 秒扫描超过 2 分钟仍 pending 的记录。 |
| `fuchang:ticket:order-idem:{userId}:{operation}:{sha256(idempotencyKey)前16字节}` | String；24h | `order_id`（以字符串储存，避免 Lua 精度问题） | Lua 最先读取。不存在才扣库存并 SET；同 key 再次请求返回已有 order。`operation` 是 `normal` 或 `rush`，故普通购票和秒杀不会互相冲突。回滚 pending reservation 时删除。 |
| `fuchang:ticket:idem-result:{userId}:{idempotencyKey}` | String；10 分钟 | `{orderID}\|{status}` | 下单成功或 MySQL 回源命中后回填。只是快速返回缓存，命中后仍会查 MySQL 取新状态。丢失不会破坏幂等。 |

### 订单相关、但不参与扣库存的 Key

| Key 模板 | 类型 / TTL | 作用 |
| --- | --- | --- |
| `fuchang:orders:user:{userId}:ver` | String / 24h | 订单列表/详情版本号；写订单或改状态时 `INCR`，让旧缓存自然失效。 |
| `fuchang:orders:list:...`、`detail:...`、`count:...` | String JSON / 约 8 秒（带抖动） | 订单读缓存；不是一致性或幂等机制。 |
| `fuchang:catalog:*` | String JSON / 5 秒到 15 分钟 | 活动目录缓存（活动详情约 45 秒、目录 meta 15 分钟等）。 |

其他非订单 Redis：登录 refresh token、分布式写限流 `fuchang:rl:write:*`、评论缓存/点赞/限流 `fuchang:comment:*`、漏斗去重及版本 `fuchang:funnel:*`。它们不属于票务下单补偿。

## 3. MQ / Outbox

### 正式确认事件

只有一个订单 Outbox 事件类型：`ticket.order.finalize`。

```text
MySQL transaction
  INSERT ticket_order(status=queued)
  INSERT ticket_order_outbox(status=pending, event_id, payload)
COMMIT
       ↓
Outbox workers（4 个默认）用 SELECT ... FOR UPDATE SKIP LOCKED 认领
pending → publishing
       ↓ publisher confirm
RabbitMQ default exchange → fuchang.order.queue
       ↓
Consumer（默认 6 worker，prefetch=5）
  INSERT ticket_order_consumer_inbox(ticket-order-finalizer,event_id)
  扣 MySQL 分桶库存
  queued → pending_payment
COMMIT → ACK
```

`TicketOrderMessage` 的字段为：`event_id,event_type,order_id,user_id,ticket_tier_id,quantity,rush_sale_campaign_id,stock_bucket_no,rush_bucket_no,seat_ids,trace_context`。

Outbox 自身状态机：`pending → publishing → published`；投递错误回 `pending` 并增加 `attempts`，达到 20 次后 `failed`，随后将仍 `queued` 的订单标为 `failed` 并回补 Redis 库存。启动时所有遗留 `publishing` 会重置为 `pending`。

RabbitMQ 结构（当前 `config/config.yaml`）：

- 主队列：`fuchang.order.queue`
- 消费重试队列：`fuchang.order.retry`，消息 header `x-retry-count` 加一；默认最多 3 次。
- 超过次数：投递到 `fuchang.order.dlx` 交换机，落入 `fuchang.order.dead`；同时订单走 `FinalizeFailedMessage`。
- 支付超时是第二条独立 MQ 链路：`fuchang.order.delay`（消息 TTL）死信转发到交换机 `fuchang.order.timeout.ex`，再到 `fuchang.order.timeout`；它只条件取消仍为 `pending_payment` 的过期订单。

## 4. 状态机

### 主订单状态

```text
queued
  ├─ Consumer 成功扣 MySQL 库存 → pending_payment
  └─ consumer/outbox 永久失败 → failed

pending_payment
  ├─ 支付回调 success → paid
  └─ 用户取消 / 延时队列或扫描器超时 → cancelled

paid
  └─ 退款成功且电子票未核销 → cancelled
```

这正是 `models/ticket_order.go` 中允许的转换；`failed` 和 `cancelled` 是终态。

支付状态是另一条轴，不能与订单状态混为一谈：

```text
unpaid → paid → refunding → refunded
   └── payment callback failure 时可变 failed
```

`payment_transaction.status` 又是第三条更贴近支付网关的状态：`pending → success/failed/closed/refunded`。

### Redis reservation 状态

```text
不存在
  └─ Lua 扣 Redis 库存、写 Hash、加入 ZSET → pending（无 TTL，等待确认或恢复）
       ├─ MySQL 订单+Outbox 已提交 → committed（Hash 保留 24h，移出 ZSET）
       └─ 下单事务确认未提交/恢复任务取得回滚权 → 删除 Hash、删除幂等映射、回加库存
```

### 辅助状态机

- `ticket_order_outbox`：`pending → publishing → published/failed`。
- `session_seat`：`available → held → sold`；取消 `held/sold → available`。`off_sale` 只是展示覆盖态，不是数据库库存态。
- 计数票档：`on_sale → waitlist → on_sale`。所有分桶卖空时进入 `waitlist`；候补队列派空且仍有余票时恢复 `on_sale`。选座票档不支持候补，保留 `sold_out`。
- 候补：`pending_payment → queued → fulfilled`，也可到 `cancelled/expired`；`queued` 的先后由 `paid_at, id` 决定，即付款成功者优先。
- 秒杀活动：`draft → scheduled → active → ended/cancelled`。

## 幂等：源码中的三层，不要混淆

| 层 | key / 唯一约束 | 解决什么 | 请求参数不一致时 |
| --- | --- | --- | --- |
| HTTP 订单结果缓存 | `fuchang:ticket:idem-result:{user}:{clientIdemKey}` | 快速返回同一订单 | 回源 MySQL 比对 `request_hash`。 |
| Redis 库存 reservation 映射 | `fuchang:ticket:order-idem:{user}:{normal/rush}:{hash(clientKey)}` | 避免网络重试再次扣 Redis 库存 | Lua 比对 reservation Hash 的 `request_hash`，不一致返回冲突码 `-5`。 |
| MySQL 最终兜底 | `UNIQUE(user_id,idempotency_key)` | 即使 Redis 丢失/重启/请求并发，也只能落一张订单 | `ticket_order.request_hash` 不同会拒绝。 |

客户端传 `Idempotency-Key`（代码要求去掉空格后至少 8 个字符），后端还对实际业务请求算 `request_hash`：普通购票为 `ticket_tier_id + quantity + purchase`，秒杀为 `campaign_id + quantity + purchase`。因此：**同一个 Idempotency-Key 只能重试同一份请求，不能换票档或数量。**

## 补偿：当前代码的三个真实窗口

1. **Redis Lua 已预扣，MySQL 下单事务失败或进程中断。** reservation 仍为 `pending`；恢复任务扫描 ZSET。它先查订单，再通过 `ticket_stock_recovery_fence(order_id)` 与下单事务竞争：`owner=order` 则确认 reservation；`owner=recovery` 才执行回滚 Lua。回滚 Lua 同时回加库存、删除 reservation、从 ZSET 移除、删除 order-idem 映射；秒杀还回加活动库存并回退用户限购计数。
2. **订单已提交，但 reservation 尚未来得及 confirm。** 恢复任务查到订单，调用 confirm Lua：`pending → committed`，不回加库存。故 confirm 失败不影响正确性，只会留下可恢复的 pending。
3. **MQ/Outbox 永久失败，或 consumer 发现不可恢复的 MySQL 库存异常。** `FinalizeFailedMessage` 条件把 `queued → failed`；若不是选座订单则回加 Redis 热库存（秒杀还回退限购计数）。已经成功进入 `pending_payment` 的取消/超时会在 MySQL 事务里先恢复分桶库存，再回加 Redis。

这里没有一个“统一 Saga 表”。补偿凭证是 Redis reservation Hash；“谁有权回滚”的并发裁决是 MySQL recovery fence；消息重复由 MySQL Inbox 消除。

## 建议你亲自走读的顺序

1. 先只读 `models/ticket_order.go`，牢记订单、Outbox、Inbox、Fence 四张表与三个状态机。
2. 读 `ticket_stock_reservation.go` 的四段 Lua：reserve normal、reserve rush、confirm、rollback。
3. 回到 `CreateOrder` / `RushSaleService.Execute`，看 Lua 成功后如何与 `createOrderAndOutbox` 接上。
4. 读 `ProcessOrderTask`：先 Inbox，后 `FOR UPDATE` 订单，扣 MySQL bucket，最后变 `pending_payment`。
5. 最后读 `FinalizeFailedMessage`、`RecoverStaleStockReservations`、`cancelPendingPaymentOnlyDB`，它们就是全部关键补偿出口。
