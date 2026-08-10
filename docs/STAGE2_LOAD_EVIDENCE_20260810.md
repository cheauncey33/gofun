# 第二阶段链路压测证据（2026-08-10）

本文记录第二阶段原型的真实压测结果。这里的 HTTP QPS 只代表入口请求处理能力，不代表订单最终确认能力；最终判断必须同时看订单消费者、RabbitMQ、Outbox、库存预约状态和排空结果。

## 第二阶段当前实现

- `ticket_order` 增加 `inventory_desired_state`、`inventory_applied_state`、重试时间和错误字段。
- 订单消费者在批处理开关打开时，只在订单事务中写入 `desired_state=reserved`，不再逐单更新库存桶。
- 取消/支付超时把目标状态改为 `released`，库存桶由批处理器合并恢复。
- 批处理器按库存桶聚合后更新 `ticket_tier_bucket`、`rush_campaign_bucket`，再推进订单的 `applied_state`。
- 批处理器先无锁读取候选订单 ID，再按 `ticket_order.PRIMARY` 精确加锁；避免对 `idx_ticket_order_inventory_work` 做范围 `FOR UPDATE`。
- `INVENTORY_BATCH_ENABLED` 默认关闭，仍可通过 Docker Compose 环境变量回退到当前同步扣减链路。

相关代码：

- `backend/migrations/008_inventory_reservations.sql`
- `backend/models/ticket_order.go`
- `backend/service/ticket_order_service.go`
- `backend/service/inventory_reservation_batch.go`
- `tests/load/k6/run_full_chain_baseline.ps1`

## VUS=100 对比

固定条件：Docker 网络内运行 k6，持续 20 秒，16 个库存桶，prefetch=5，支付超时消费者=2，MySQL `max_connections=300`，应用连接池 `max_open_conns=100`。

| 配置 | HTTP req/s | HTTP p99 | HTTP 5xx | 消费速率（约） | 事务平均 | 锁等待增量 | HTTP 窗口 MQ 峰值 | 60 秒后状态 |
|---|---:|---:|---:|---:|---:|---:|---:|---|
| 第一阶段同步，消费者 6，Outbox 4 | 1404.0 | 187.1ms | 0 | 74.6/s | 68.7ms | 524 | 1827 | 作为对照 |
| 第二阶段批处理，消费者 6，Outbox 4 | 1417.1 | 179.7ms | 0 | 约 86/s | 56.45ms | 267 | 2122 | MQ 未排空；pending outbox=16798 |
| 第二阶段批处理，消费者 6，Outbox 8 | 1385.7 | 167.0ms | 0 | 约 80/s | 54.22ms | 251 | 4701 | MQ 未排空；pending outbox=5888 |
| 第二阶段批处理，消费者 8，Outbox 8 | 1360.8 | 194.9ms | 0 | 约 104/s | 59.14ms | 270 | 3839 | MQ 未排空；pending outbox=6305 |

补充结果：三轮有效压测均为 HTTP 5xx=0、transport error=0、死信=0；但三轮均未满足“主队列、Outbox、库存预约状态全部排空”。最后一轮 60 秒后仍有 `inventory_pending=21`，所以不能把 1360.8 req/s 当作稳定的最终订单吞吐。

稳定性扫描（消费者 8、Outbox 8）得到：

| VUS | HTTP req/s | HTTP p99 | HTTP 窗口主订单队列峰值 | 60 秒后 Outbox pending | 60 秒后主订单队列 | 60 秒后库存 pending |
|---:|---:|---:|---:|---:|---:|---:|
| 5 | 150.3 | 58.2ms | 3010 | 0 | 0 | 2 |
| 10 | 269.1 | 72.9ms | 5166 | 0 | 0 | 5 |

这里的 RabbitMQ `delay_queue`/`timeout_queue` 是支付超时链路的定时消息，不能当作订单消费积压；脚本已单独增加 `work_queue_total`，只把主订单队列和 retry 队列作为工作积压判断。低负载两轮主订单队列和 Outbox 都能归零，但仍有少量库存状态等待支付超时/取消链路完成，因此这只是“入口稳定性扫描”，不是最终一致性验收。

原始结果：

- [Outbox 4 / consumer 6](../tests/load/results/gofun-stage2-vus100f-20260810/vus-100/)
- [Outbox 8 / consumer 6](../tests/load/results/gofun-stage2-vus100g-20260810/vus-100/)
- [Outbox 8 / consumer 8](../tests/load/results/gofun-stage2-vus100h-20260810/vus-100/)
- [稳定性扫描 VUS=5,10](../tests/load/results/gofun-stage2-stable-scan-20260810/)

## 结论

1. 第一阶段的库存桶更新已经不是唯一主瓶颈。批处理模式把订单事务平均耗时从约 68.7ms 降到 54～59ms，锁等待增量也降到约 250～270。
2. 继续增加 Outbox publisher 只能更快把消息推入 RabbitMQ：Outbox=8 时 pending outbox 明显下降，但 MQ 峰值变大，最终仍由订单消费者和数据库事务处理能力限制。
3. 消费者 6→8 能把采样窗口消费速率从约 80/s 提高到约 104/s，但在 HTTP 约 1360/s 的入口压力下仍持续积压；当前没有证据支持进入 VUS=200。
4. 当前第二阶段应保留为实验开关，不应直接作为默认生产链路。默认仍使用已验证的同步库存扣减路径；除非后续完成库存状态对账、批处理排空和稳定负载验证。

## 下一步

- 先以消费者 8、Outbox 8 为候选配置，补一轮低于消费者能力的稳定负载，确认 MQ、Outbox、`inventory_pending` 能归零，再找可持续入口上限。
- 继续采集 `ProcessOrderTask` 事务 P95/P99、`ticket_order` 主键锁、库存批处理耗时和 Outbox publisher 实际速率。
- 若稳定入口仍明显低于目标，再进入第三阶段：按活动/库存桶路由到独立消费者组和独立库存资源；暂不引入 Nacos，先用 Compose 环境变量完成可回滚扩容。
