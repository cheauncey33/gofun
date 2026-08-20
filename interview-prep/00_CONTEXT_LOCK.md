# Gofun 票务 · 面试材料防偏离总控

本文档只描述当前 Gofun 多主办方活动票务实现。面试回答以当前代码、阶段文档和
可复现压测结果为依据。

## 依据来源

- 入口与路由：backend/main.go
- 数据模型：backend/models/
- 订单与支付：backend/service/ticket_order_service.go
- 抢票：backend/service/rush_sale_service.go
- 订单消费者与 Outbox：backend/service/ticket_order_consumer.go、
  backend/service/ticket_order_outbox_publish.go
- 库存分桶与补偿：backend/service/inventory_bucket.go、
  backend/service/ticket_compensation_service.go
- 支付沙箱：backend/service/payment_gateway.go
- 电子票核验：backend/service/ticket_verification_service.go
- 压测证据：tests/load/results/capacity-buckets-compare-20260731.md

## 项目边界

Gofun 是多主办方、无选座的活动票务平台，当前覆盖：

- 活动、场馆、场次、票档和主办方运营。
- 普通购票和限时抢票。
- Redis Lua 预扣、Outbox、RabbitMQ 异步落库。
- 支付沙箱异步签名回调、电子票签发、二维码核验和退款约束。
- Prometheus 指标、Grafana、pprof 与可选 OpenTelemetry/Jaeger。

不应说成已完成：真实支付渠道、重启后沙箱回调恢复、渠道查询/对账、生产级退款重试、
选座、离线核验、转赠和复杂财务结算。

## 普通购票链路

```text
POST /api/v1/orders
→ 校验活动、票档、数量、限购、观演人信息和幂等键
→ Redis Lua 预扣票档 quota
→ 同步写 ticket_order(queued) 与 ticket_order_outbox
→ Outbox publisher confirm 投递 RabbitMQ
→ consumer 创建 pending_payment 订单
→ 创建支付单并等待沙箱签名回调
→ 验签、校验金额和支付单状态
→ paid 后签发电子票
```

创建接口成功不等于订单已经完成落库；queued 是异步处理中，pending_payment 是待支付，
paid 才是支付成功。

## 抢票链路

```text
POST /api/v1/rush-sales/:id/execute
→ 校验开售时间、票档、活动 quota、个人限购和幂等键
→ Redis Lua 原子预扣 rush quota
→ 写订单 Outbox
→ 复用 RabbitMQ consumer 创建 pending_payment
```

当前没有用户级 Redis 分布式锁。并发控制来自幂等键、Redis Lua、MySQL 条件更新、限流和
库存分桶；回答时不要把用户锁说成当前实现。

## 一致性与补偿

- Redis 是前置库存/并发闸门，MySQL 是订单、支付单、电子票和最终 quota 的事实来源。
- publisher confirm、消费者查重和 Outbox 恢复用于降低重复投递风险。
- Redis 扣库存时同步写 pending 预扣凭证；库存恢复 Worker 扫描遗留凭证，并低频执行 Redis/MySQL quota 对账。
- 扫描未命中订单后还要竞争 MySQL `order_id` 唯一恢复栅栏，取得 recovery 权限才回滚，避免原事务随后提交造成多放库存。
- 总量对账只安全地下调 Redis 或补建无在途凭证的缺失 key；偏少只告警，不把 Redis 当最终事实。
- 支付回调、超时关单和退款跨组件，当前仍是最终一致链路。

## 压测口径

可引用 2026-07-31 单热点分桶报告：在该机器、配置和数据规模下，1500 单三轮均成功，
分桶 v2 的 MQ 追平和 row lock wait 有明显改善。只能说“已验证该场景”，不能说成生产
最大容量或长期 SLO。

## 回答规则

- 已实现：指出文件、状态、失败分支和验证方式。
- 当前限制：直接说明未完成项，不用扩展方案掩盖。
- 代码与文档冲突：以代码为准并更新文档。
