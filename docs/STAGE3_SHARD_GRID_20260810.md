# 第三阶段库存独立库网格压测证据（2026-08-10）

> 历史实验记录：分库路径已取消，本文不属于当前基线或生产配置。当前固定为“同库 + 分桶 + Consumer 直接扣库存”，`pending_payment` 仅在同库库存事务成功后进入。

> 统计修订：2026-08-10 重新修正了 PowerShell 汇总脚本的 counter 差值计算。此前版本把部分消费者速率和事务平均耗时高估；本文及同目录 `grid-summary.json` 以修订后的“末值 - 首值”结果为准。原始 `samples.json` 未修改。

## 结论先行

第二阶段在 VUS=100、消费者 8、Outbox publisher 8 时，入口约 1360 req/s，但订单消费者约 104 条/s，且压测后的库存预约和消息没有在限定时间内稳定排空。因此继续验证第三阶段是有依据的。

本轮第三阶段验证的是“订单库与库存库物理隔离”，还不是完整的“按活动/桶多库存库分片”。结果如下：

- **单库消费者 8** 的原始 HTTP 峰值最高：VUS=200 时 1556.5 req/s，但主链路没有在 90 秒排空窗口内完成收敛。
- **独立库存库 + 消费者 8** 没有带来无条件的入口提升，VUS=100/200 分别低于单库约 4.6%/6.4%。
- **独立库存库 + 消费者 12** 是本轮更适合作为后续实验基线的组合：VUS=100 达到 1420.0 req/s、VUS=200 达到 1478.3 req/s；两档都在 90 秒窗口内主链路排空，库存预约为 0，死信为 0。但订单消费者成功消息窗口速率只有约 154/s 和 117/s。
- 这不是“已经稳定达到 1000 条/秒最终订单确认”的证明。`consumer msg/s` 是订单消费者成功处理消息的 Prometheus 计数器窗口速率，压测总时长只有 15 秒；仍需做更长的稳态测试并单独统计最终订单状态。
- 诊断样本中的主要锁等待对象仍是 `ticket_order[PRIMARY]`，不是库存桶行。说明独立库存库降低了库存库与订单库之间的耦合，但当前主瓶颈仍在订单行锁/订单消费者事务，不能仅靠继续拆库存库解决。

## 本阶段实现范围

当前代码实现的是一个可回滚的独立库存数据库实验拓扑：

```text
主 MySQL
  ├─ ticket_order / outbox / payment ...
  └─ 订单状态与消息投递事务

库存 MySQL
  ├─ ticket_tier_bucket
  ├─ rush_campaign_bucket
  └─ inventory_operation
```

订单库和库存库不使用跨库两阶段提交，库存操作通过唯一操作键和幂等账本收敛：

```text
订单消费者/批处理器
  -> 主库锁定待处理订单
  -> 库存库插入 inventory_operation（唯一键，重复投递不重复扣减）
  -> 库存库按桶更新
  -> 主库推进 inventory_applied_state
  -> 对账/重试处理跨库提交窗口
```

相关实现：

- `backend/container/container.go`、`backend/container/init.go`：建立独立 `InventoryDB` 连接，并只迁移库存相关表。
- `backend/config/config.go`、`backend/config/config.example.yaml`：`inventory.shard_enabled` 和 `inventory.shard_dsn` 配置。
- `backend/models/inventory_operation.go`：库存操作幂等账本。
- `backend/service/inventory_reservation_batch.go`：独立库存库批处理和跨库提交顺序。
- `backend/service/inventory_bucket_ops.go`：不更新主库父级库存状态的库存桶操作。
- `tests/integration/docker-compose.ticketing.yml`、`tests/load/docker-compose.capacity.yml`：`mysql_inventory` 服务。

当前仍是“一个独立库存库承载所有库存表”，尚未实现以下完整能力：

- 按 `campaign_id + bucket_no` 路由到多个库存库；
- 每个库存分片独立 RabbitMQ consumer group；
- 分片路由元数据、路由版本和迁移工具；
- 分片级故障隔离与跨分片对账面板。

所以本轮结果只能证明“库存从订单库分离后是否值得继续”，不能直接作为多分片生产架构的性能承诺。

## 压测口径

- 每个配置使用独立 Compose project、独立 MySQL/Redis/RabbitMQ volume，结束后清理测试栈。
- k6 运行在 Compose 网络内，访问 `http://backend:8080/api/v1`，不经过 Windows 宿主机端口转发。
- VUS：50、100、200；持续 15 秒；排空窗口 90 秒。
- 开启第二阶段批处理、8 个库存桶；每组使用 Outbox publisher=8。
- 三组配置：

| 配置 | 主订单消费者 | Outbox publisher | 库存拓扑 |
|---|---:|---:|---|
| `single-db-c8-p8` | 8 | 8 | 订单与库存同一 MySQL |
| `inventory-db-c8-p8` | 8 | 8 | 库存独立 MySQL |
| `inventory-db-c12-p8` | 12 | 8 | 库存独立 MySQL |

入口成功率只统计 HTTP 请求；主链路排空同时检查工作队列、Outbox pending、库存 pending 和死信。RabbitMQ 的支付延时/超时定时队列不计入订单工作队列积压。

## 实测结果

事务 P95 是 Prometheus histogram 的桶上界：100ms 表示 P95 落在 `(50ms, 100ms]`，250ms 表示落在 `(100ms, 250ms]`，不是精确插值结果。

| 配置 | VUS | HTTP req/s | 消费消息/s | 事务平均/P95 | HTTP p99 | 工作 MQ 峰值 | 排空阶段采样峰值 | inventory pending | 90s 内主链路排空 | 死信 | `ticket_order` 锁等待 |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---|---:|---:|
| single-db-c8-p8 | 50 | 937.3 | 125.8 | 41.6ms / 100ms | 134.3ms | 1762 | 3698 | 2 | 否 | 0 | 62 |
| single-db-c8-p8 | 100 | 1397.5 | 105.2 | 58.6ms / 250ms | 210.8ms | 2026 | 4938 | 0 | 否 | 0 | 216 |
| single-db-c8-p8 | 200 | **1556.5** | 79.4 | 78.5ms / 250ms | 320.3ms | 1304 | 4978 | 18 | 否 | 0 | 244 |
| inventory-db-c8-p8 | 50 | 906.7 | 143.4 | 42.0ms / 100ms | 170.6ms | 1754 | 3461 | 7 | 否 | 0 | 74 |
| inventory-db-c8-p8 | 100 | 1333.9 | 103.8 | 60.0ms / 100ms | 248.5ms | 1789 | 4907 | 0 | 是 | 0 | 213 |
| inventory-db-c8-p8 | 200 | 1457.0 | 84.6 | 66.9ms / 250ms | 357.8ms | 1591 | 5055 | 26 | 否 | 0 | 303 |
| inventory-db-c12-p8 | 50 | 900.0 | 197.2 | 43.6ms / 100ms | 143.0ms | 964 | 1001 | 6 | 否 | 0 | 71 |
| inventory-db-c12-p8 | 100 | **1420.0** | 153.9 | 55.9ms / 250ms | 215.3ms | 1418 | 1013 | 0 | 是 | 0 | 300 |
| inventory-db-c12-p8 | 200 | 1478.3 | 117.3 | 79.2ms / 250ms | 333.4ms | **935** | 905 | 0 | 是 | 0 | 256 |

说明：

- `consumer msg/s`、事务平均/P95来自诊断窗口的 Prometheus counter/histogram 差值；这里使用的是“末值 - 首值”，不是直接读取 counter 末值。VUS=50 的窗口包含启动阶段，不能当作稳定消费上限。
- `工作 MQ 峰值`是 HTTP 压测窗口内的主订单队列 + retry 队列峰值。
- `排空阶段采样峰值`用于观察压测结束后的残留工作量，不等同于 HTTP 窗口峰值。
- 这三组有效请求均为 HTTP 成功率 100%、HTTP 5xx=0、transport error=0、死信=0；这只代表本轮测试没有观测到请求/消息错误，不代表没有容量边界。

原始数据：

- [网格汇总 Markdown](../tests/load/results/stage3-grid-20260810-final2/grid-summary.md)
- [网格汇总 JSON](../tests/load/results/stage3-grid-20260810-final2/grid-summary.json)
- [网格运行器](../tests/load/k6/run_stage3_grid.ps1)
- [汇总脚本](../tests/load/k6/merge_stage3_grid.ps1)

## 分析与决策

### 1. 是否达到入口峰值目标

如果当前目标是“HTTP 入口达到 1000 req/s 且本轮无 5xx”，VUS=100 和 VUS=200 的三组配置都达到了这个局部目标。

如果目标是“最终订单链路能够持续处理并快速追平”，不能只看入口 QPS。当前真实订单消费者成功消息速率仍只有约 80～154/s（VUS=100/200 的主要档位），不能解释成每秒处理 1000 个最终订单：

- 单库 c8 在 VUS=200 的入口 QPS 最高，但 90 秒内没有排空，库存 pending 仍为 18。
- 独立库存库 c12 在 VUS=100/200 都排空，且 inventory pending=0、死信=0；它比独立库存库 c8 更值得作为下一轮稳态基线。
- 但 c12 的 HTTP p99 在 VUS=200 已为 333.4ms，订单消费者速率约 117.3/s，事务平均 79.2ms、P95 落在 250ms 桶，且主订单锁等待仍为 256，说明“排空改善”不等于“主事务热点已经消失”。

### 2. 主要瓶颈是否已经转移

没有证据表明库存桶已经成为唯一瓶颈。诊断中的主要竞争对象仍是：

```text
ticket_order[PRIMARY] <- block=ticket_order[PRIMARY]
```

这表示下一步优先级应是缩短订单消费者在 `ticket_order` 上的锁持有时间、减少重复订单状态竞争、拆分订单状态推进和库存预约确认的耦合，并补充按 SQL 的锁等待统计。不能因为独立库存库在某一档排空更好，就直接继续增加消费者或引入 Nacos。

### 3. 第三阶段是否收敛为默认配置

暂不把 `INVENTORY_SHARD_ENABLED=true` 收敛为默认值，原因是：

1. 独立库存库 c8 的 HTTP 入口没有稳定优于单库 c8；
2. 当前实现仍是一个独立库存库，不是多分片路由；
3. 跨库没有 2PC，必须依靠幂等账本、重试、对账和故障恢复验收；
4. 真实瓶颈仍有 `ticket_order` 主键锁竞争，分离库存不能单独解决。

建议暂定：

- **单机基线**：保留第二阶段 batch + consumer=8 + publisher=8，作为对照和回退档。
- **第三阶段实验基线**：独立库存库 + batch + consumer=12 + publisher=8，仅用于继续测量和验证故障恢复。
- **暂不执行**：VUS=500、Nacos 动态扩容、Redis Cluster、多库存库分片。先完成更长稳态、库存库断连恢复、主订单锁 SQL 归因。

## 下一轮测试与改造顺序

1. 以 `inventory-db-c12-p8` 做 60 秒稳态扫描，至少跑 VUS=100、200；记录 HTTP p50/p95/p99、订单成功事务速率、订单状态最终数量、工作队列面积和排空时间。
2. 给 `ticket_order` 竞争 SQL 增加按语句/阶段的耗时与锁等待归因，确认是订单查重、订单状态 UPDATE、items 读取还是支付超时竞争。
3. 做 consumer=8/12/16 的小范围 A/B；只有在事务平均/P95、锁等待和排空时间同步改善时才继续扩消费者。
4. 验证独立库存库重启、连接拒绝、重复投递、主库提交失败后的账本重放和对账；任一项出现未解释差异，第三阶段继续保持实验开关。
5. 只有单个独立库存库仍达到资源瓶颈，且单库订单锁已不是主瓶颈时，才继续实现 `campaign_id + bucket_no` 到多个库存库的真实路由。

复现命令：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File `
  tests/load/k6/run_stage3_grid.ps1 `
  -Vus 50,100,200 `
  -Duration 15s `
  -DrainSeconds 90 `
  -FastPrepare
```
