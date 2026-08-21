# 一致性设计：从抢票入口到落单

> 目标：在高并发抢票下，保证 **不超卖、不重复下单、消息不丢、最终一致**。
> 本文描述 Gofun 票务「Redis 预扣 → 事务 Outbox → MQ → 库存事务 → pending_payment」全链路的可靠性假设与每步失败的处理。
> 所有结论均可在 `backend/service/` 下找到对应实现。

## 设计前提

- **MySQL 是最终事实来源**；Redis 只是前置并发闸门，不承担正确性。
- MQ 采用 **至少一次（at-least-once）投递**，因此「重复消费」是常态，不是异常——所有消费必须幂等。
- 不要求强分布式事务（无 XA / Saga 编排）；用「同事务写 + 异步投递 + 幂等消费 + 周期对账」达到最终一致。

## 全链路时序

```text
[用户] POST /rush-sales/:id/execute  (X-Idempotency-Key)
   │
   ▼ ① 入口：写限流 + 幂等键校验
[TicketOrderService.CreateOrder / RushSaleService.Execute]
   │  查幂等：Redis 缓存命中 → 直接返回已有凭据
   ▼ ② Redis Lua 原子预扣
   │  一次性校验并扣减：活动票额 + 票档库存 + 个人限购（分桶选桶）
   │  失败 → 返回售罄/限购，不写库、不发消息
   ▼ ③ MySQL 事务：写恢复栅栏 + 订单(queued) + Outbox 行
   │  三者同一主库事务提交，HTTP 不等待 Publisher/MQ
   ▼ ④ 返回「排队中」给用户
   │
   ▼ ⑤ Outbox publisher（多 worker）
   │  SKIP LOCKED 抢占 pending 行 → 发 MQ → 等 broker confirm → 标记 published
   ▼ ⑥ RabbitMQ（durable queue + persistent message）
   ▼ ⑦ Consumer（手动 ack）
   │  主库同一事务：订单 FOR UPDATE → 分桶扣减 → queued → pending_payment
   ▼ ⑧ 投递支付超时延时消息（失败由扫描器兜底）
   ▼ ⑨ 支付 / 超时关单 → 出票 / 释放库存
   ▼ ⑩ 库存恢复 Worker：恢复 pending 预扣，并周期对齐 Redis 与 MySQL 票额
```

## 每一步的失败处理

| 步骤 | 可能失败 | 处理 | 一致性保证 |
|------|----------|------|-----------|
| ① 幂等校验 | 重复提交 / 网络重试 | Redis 缓存 + DB `user_id+idempotency_key` 唯一约束，命中直接返回原凭据 | 同一幂等键只建一单 |
| ② Redis 预扣 | 库存不足 / 超限 | Lua 原子返回失败码，不落库 | 入口层面挡住超卖 |
| ③ 写栅栏+订单+Outbox | 事务失败或提交结果未知 | Redis pending 凭证进入恢复；恢复任务和订单事务竞争同一个 `order_id` 唯一栅栏，取得 `recovery` 后才允许回滚 | 不会因查不到未提交订单而提前归还库存 |
| ⑤ Outbox 投递 | broker confirm 失败/超时 | 行状态回 `pending` 等下轮；超过最大次数标 `failed` 并触发 `FinalizeFailedMessage` | 不丢、可观测 |
| ⑥ MQ 存储 | broker 重启 | durable queue + persistent message | 消息不丢 |
| ⑦ 消费处理 | 可重试错误（如行锁超时） | 重发 retry 队列，`x-retry-count` 递增，超上限进 **DLQ** | 至少一次，最终人工/自动介入 |
| ⑦ 消费处理 | 不可重试错误（凭据不存在/消息不一致） | `FinalizeFailedMessage` + ack，不再重试 | 快速失败，不阻塞队列 |
| ⑦ 重复消费 | MQ 重投 / retry 重投 | 依赖订单行 `FOR UPDATE` 和非 queued 状态判断 | 重复消费不重复扣库存 |
| ⑧ 超时消息 | 投递失败 | DB 扫描器周期兜底关单 | 超时不泄漏库存 |
| ⑩ 库存恢复与对账 | Redis pending 遗留或 Redis/MySQL 漂移 | 同一 Worker 高频恢复单笔凭证、低频做总量对账；偏多用 Lua CAS 下调，偏少只告警，不猜测性加库存 | 以单笔凭证精确恢复，以总量对账兜底 |

## 幂等的三层防线

1. **客户端幂等键**：前端生成 `X-Idempotency-Key`（uuidv4），同一笔请求重试携带同键。
2. **入口去重**：Redis 缓存命中直接返回；DB `(user_id, idempotency_key)` 唯一约束兜底并发。
3. **消费幂等**：Consumer 在主库事务内锁订单行 `FOR UPDATE`；订单已不是 `queued` 时直接幂等返回。

## 不超卖的两个层级

- **入口层**：Redis Lua 原子扣减，先把超卖挡在 DB 之外。
- **落库层**：MySQL 条件 UPDATE `WHERE remaining >= ?`（分桶时作用在命中的桶行），`RowsAffected==0` 即判定库存不足。两层独立，任何一层失效另一层仍能兜底。

## 为什么用 Outbox 而不是「下单时直接发 MQ」

直接发 MQ 存在经典的双写不一致：**DB 提交成功但 MQ 发送失败**（或反之），订单与消息就会漂移。Outbox 让「订单 + 待投递消息」落在**同一个本地事务**里，再由独立 publisher 异步、可重试地把消息送达 broker——把分布式一致性问题降级为「本地事务 + 至少一次投递 + 幂等消费」，这是工程上更可控的组合。

## 库存恢复 Worker

- Redis Lua 在扣减库存时同步写入 pending 预扣凭证；HTTP 成功后确认凭证，明确失败后回滚。pending Hash 在确认前不设 TTL，避免凭证过期后只清 ZSET、库存无法按 quantity 归还。
- Worker 每 30 秒扫描超过宽限期的 pending 凭证，每 5 分钟在同一循环内执行一次总量对账。启动时的 `RecoverAllStockReservations` 同样只处理过期凭证，避免滚动发布回滚其他实例的在途预扣。
- 扫描器一次查不到订单并不等于可以回滚。它必须尝试写入 `ticket_stock_recovery_fence(owner=recovery)`；订单事务会写入同一个 `order_id` 的 `owner=order`。InnoDB 唯一键冲突会等待先到事务结束，因此两者只有一方能取得处理权。
- 全量对账使用 `MySQL remaining_quota - queued 占用` 计算安全可用量。Redis 偏多时通过“值未变化才下调”的 Lua 修复，且只在真正写入后计数；key 缺失且存在 pending 时不重建；Redis 偏少只记录异常，由单笔凭证恢复，不直接增加。`WarmTicketQuota` 跳过仍有 pending 的库存 key。

面试表述可以收敛为：**用 Worker 定时扫描 Redis 预扣凭证并做库存对账，通过 MySQL 唯一栅栏解决事务提交与补偿回滚竞态，以更简单、可控的方式实现最终一致性。**

## 已知边界（诚实声明）

- 支付当前使用内置 `SandboxPaymentGateway`，不读取或修改 `user.balance_cents`：平台落库支付单，沙箱异步回调后才把订单改为 `paid` 并签发电子票。回调会校验签名、金额、provider 和支付单状态，但调度状态仍在进程内。
- 支付当前不是生产级真实渠道：重启恢复、渠道查询、对账、退款重试以及支付回调与超时的更强并发保护仍需补齐。外部退款成功与本地事务也不是一个分布式事务，必须通过补偿任务收敛。
- 多副本部署下 Outbox publisher / timeout scanner 通过 `SKIP LOCKED` 抢占，天然支持多实例，但需注意单消息只被一个 worker 处理。
- `pending_payment` 只表示主库事务已提交分桶库存 reserve、等待支付；订单进入该状态前不能只写预约意图。
- 一致性是**最终一致**，毫秒级窗口内 Redis 与 MySQL 可能短暂不一致，由补偿对账收敛。
