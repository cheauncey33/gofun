# Gofun 三阶段库存与消费链路改造方案

> 状态：执行方案，基于 2026-08-10 当前代码和 Docker 网络压测结果。
> 范围：当前票务路径，包括抢票入口、Redis 预扣、Outbox、RabbitMQ、订单消费者、MySQL 库存和支付超时释放。
> 原则：先保证不超卖、不丢消息、可对账，再扩大吞吐；入口 HTTP QPS、MQ 消费速率和最终订单能力必须分开统计。

> 当前决策：分库与 Batch Saga 实验均已取消；基线和生产配置固定为“同库 + 分桶 + Consumer 直接扣库存”。下文涉及第三阶段分库的内容仅保留为历史方案背景，不代表待实施路径。

## 1. 当前结论

当前链路不是 HTTP 入口慢，而是 MQ 后的 MySQL 订单确认速度低。

最近一次干净压测使用：

- VUS=100，持续 20 秒；k6 运行在 Docker Compose 网络内；
- 库存分桶开启，8 个桶；
- Outbox batch，publisher=4；
- 订单消费者=6，prefetch=5；支付超时消费者=2；
- MySQL `max_connections=300`，应用连接池 `max_open_conns=100`。

结果：

| 指标 | 结果 | 口径 |
|---|---:|---|
| HTTP 入口 | 1401.7 req/s | k6 收到的入口响应 |
| HTTP p99 | 177.3ms | 入口延迟 |
| HTTP 5xx | 0 | 本轮无服务端错误 |
| 主订单成功消费 | 约 73.4 条/s | `mq_messages_consumed_total{result="success"}` |
| 消费事务平均耗时 | 约 70.5ms | 成功事务 |
| RabbitMQ 峰值 | 2468 条 | ready + unacknowledged |
| 当前锁等待峰值 | 3 | `Innodb_row_lock_current_waits` |
| 锁等待增量 | 734 | 本轮采样窗口 |
| 60 秒后是否排空 | 否 | 观察窗口到期，不代表永久不排空 |

原始结果：[gofun-stage1metrics2-20260810/vus-100](../tests/load/results/gofun-stage1metrics2-20260810/vus-100/)。

### 1.2 执行进度（2026-08-10）

第二阶段批处理在消费者 8、publisher 8 时仍未证明高峰后能稳定排空，因此已按本方案进入第三阶段验证。当前完成的是“订单库 + 一个独立库存库”的可回滚实验拓扑，并完成 VUS=50/100/200 的 grid 扫描；还没有实现按活动/桶拆成多个库存库的完整分片路由。

实测证据见：[第三阶段独立库存库网格压测证据](STAGE3_SHARD_GRID_20260810.md)。当前建议把 `inventory-db-c12-p8` 作为后续稳态测试基线，把 `single-db-c8-p8` 作为回退对照；暂不把独立库存库切成默认生产配置，也暂不直接进入 VUS=500。

### 1.1 本轮阶段观测与 8/16 桶 A/B

为避免只看总事务耗时，本轮在 `ProcessOrderTask` 内增加了固定阶段和桶号维度的 Prometheus 指标，并用相同 Docker 网络、VUS=100、20 秒、消费者=6、prefetch=5、Outbox publisher=4、MySQL max_connections=300 重跑。

| 配置 | HTTP req/s | HTTP p99 | 主订单消费速率 | 事务平均 | rush bucket 更新平均 | 锁等待增量 | MQ HTTP 窗口峰值 |
|---|---:|---:|---:|---:|---:|---:|---:|
| 8 桶 | 1401.7 | 177.3ms | 73.4/s | 70.5ms | 12.7ms | 734 | 2468 |
| 16 桶 | 1404.0 | 187.1ms | 74.6/s | 68.7ms | 7.1ms | 524 | 1827 |

16 桶把 `rush_campaign_bucket` 的更新耗时和锁等待降低了，但入口 QPS 基本没有变化，说明当前主要受异步事务模型和单库提交能力限制，不能把“加桶”宣传成入口吞吐翻倍。当前结论是：

- 新建压测活动可把 16 桶作为候选容量配置；已有 8 桶数据不能只改配置，需要停开售/排空后重分桶或迁移；
- 消费者维持 6，8 只作为恢复/排空档，不再盲目增加；
- 下一步进入第二阶段的库存事件明细与批量合并原型，目标是减少每条订单都更新 MySQL 库存热行；
- 16 桶 A/B 原始结果：[gofun-bucket16-20260810/vus-100](../tests/load/results/gofun-bucket16-20260810/vus-100/)，8 桶阶段结果：[gofun-stage1metrics2-20260810/vus-100](../tests/load/results/gofun-stage1metrics2-20260810/vus-100/)。

当前锁等待对象主要是：

```text
rush_campaign_bucket.PRIMARY
ticket_order.PRIMARY
```

这说明当前瓶颈主要是数据库热行和事务并发，而不是 Redis、RabbitMQ 或 Go goroutine 的原始处理能力。MySQL CPU 较低也不能证明数据库有同等比例的空闲吞吐：等待行锁的连接会占用连接和事务资源，但不一定消耗 CPU。

## 2. 三阶段总览

| 阶段 | 目标 | 核心改动 | 是否分库 | 进入下一阶段的条件 |
|---|---|---|---|---|
| 第一阶段 | 先把当前单库事务压短、把锁来源测清楚 | SQL 分段观测、幂等确认、减少事务内查询和无效写 | 否 | 仍受库存桶热行限制，或消费速率提升不足 |
| 第二阶段 | 不再逐条消息更新库存汇总行 | 库存事件明细、批量合并、对账补偿 | 否，先单库验证 | 数据正确且单库批量模型仍达不到目标 |
| 第三阶段 | 把超级热点活动拆到独立物理资源 | 按活动/库存桶分片、独立数据库和消费者组 | 是 | 仅在第二阶段仍无法满足目标时执行 |

不要把“增加消费者数量”单独当作一个阶段。消费者数量只是每个阶段内的受控参数；在共享热点行没有消失前，盲目增加消费者只会提高锁等待。

## 3. 第一阶段：当前单库事务优化

### 3.1 当前事务边界

当前 `ProcessOrderTask` 位于 [ticket_order_service.go](../backend/service/ticket_order_service.go:583)，主要流程是：

```text
开启 MySQL 事务
  -> ticket_order FOR UPDATE
  -> 查询 ticket_order_item
  -> 校验用户、票档、数量、活动
  -> 更新 rush_campaign_bucket
  -> 更新 ticket_tier_bucket
  -> 必要时判断售罄状态
  -> ticket_order: queued -> pending_payment
提交事务
  -> 发布支付超时消息
  -> 推送订单状态事件
```

当前已经完成的减法：

- 订单锁定查询不再预加载完整订单明细；
- 父级 `ticket_tier` 不再每单更新库存数量；
- 普通扣减不更新父级状态；
- 只有全部库存归零时才执行 `on_sale -> sold_out`；
- 只有库存桶从 0 恢复到正数时才执行 `sold_out -> on_sale`；
- 父级汇总由补偿任务周期修正；
- `PublishPaymentTimeout` 在事务提交后执行。

库存桶操作位于 [inventory_bucket_ops.go](../backend/service/inventory_bucket_ops.go:66)。

### 3.2 第一阶段待做任务

#### A. 把事务耗时拆成 SQL 级指标（已实施）

现有总事务直方图还不够，需要区分：

```text
ticket_order_lock_seconds
ticket_order_item_read_seconds
rush_campaign_bucket_update_seconds
ticket_tier_bucket_update_seconds
ticket_order_state_update_seconds
ticket_order_transaction_commit_seconds
ticket_order_retry_total
```

每个指标至少带以下标签：

```text
result = success | retryable_error | permanent_error
campaign_id 或脱敏后的热点分组
bucket_no
```

不要把完整订单号、用户 ID 作为 Prometheus label，避免高基数。

当前实际指标名为：

```text
ticket_order_consumer_stage_duration_seconds{stage}
ticket_order_consumer_inventory_bucket_duration_seconds{kind,bucket_no}
ticket_order_consumer_inventory_bucket_operations_total{kind,bucket_no,result}
```

阶段观测确认 `rush_bucket_update` 是当前事务内最重的库存步骤，但它不是单一桶长期独占；因此继续增加消费者不会消除共享数据库写入成本。

#### B. 查清 `ticket_order.PRIMARY` 为什么竞争

订单 ID 通常不同，理论上不应该像库存桶一样成为长期热点。需要区分：

1. RabbitMQ 重复投递导致同一订单被多个消费者同时处理；
2. 订单消费者和支付超时/取消流程同时处理同一订单；
3. Outbox 恢复或重试重复发布；
4. 订单状态查询和状态更新的索引扫描造成等待；
5. 采样脚本看到的是短时偶发锁，而不是主瓶颈。

每次锁等待必须保存：

```text
requesting_table/index/lock_data
blocking_table/index/lock_data
requesting_sql
blocking_sql
order_id/campaign_id/bucket_no（如能从 trace 关联）
```

在没有完成这一步前，不直接删除 `FOR UPDATE`。简单删除订单行锁，可能导致重复消息同时扣库存。

#### C. 安全评估订单行锁替代方案

候选方案不是把订单事务拆成两个互不相关的事务，而是在同一事务内引入幂等确认：

```text
读取/确认订单消息
  -> 通过唯一 order_id/reservation_id 抢占处理权
  -> 只有抢占成功的消息继续库存确认
  -> 订单状态条件更新
```

如果没有唯一处理记录或等价的原子状态机，不允许直接改成“先扣库存、再更新订单”的两段事务。

#### D. 固定其余变量，重新做 6/8 消费者对比

第一阶段验证必须固定：

- VUS=100，先不进入 500/1000；
- Docker 网络内 k6；
- MySQL `max_connections=300`；
- 应用 `max_open_conns=100`、idle=10；
- prefetch=5；
- Outbox publisher=4、batch=200；
- 支付超时消费者=2；
- 每轮重新准备活动、用户和队列。

只比较：

```text
order consumer = 6 vs 8
```

记录：

- HTTP QPS、p99、5xx、transport error；
- 主订单消费速率；
- ProcessOrderTask 平均/P95/P99；
- `rush_campaign_bucket` 锁等待；
- `ticket_order` 锁等待；
- retry、dead letter、Outbox pending；
- MQ 峰值和排空情况；
- 每个 bucket 的命中和失败次数。

### 3.3 第一阶段动态决策

| 结果 | 动作 |
|---|---|
| 有 1040、连接拒绝或 MySQL 初始化错误 | 该轮无效，先处理连接预算和容器配置 |
| 有 HTTP 5xx、死信或库存不一致 | 停止性能比较，先定位正确性问题 |
| 锁对象仍是 `rush_campaign_bucket` | 不再盲目增加消费者，进入第二阶段设计 |
| 锁对象主要是 `ticket_order` | 先处理幂等/订单状态竞争，不先分库 |
| 锁等待明显下降且消费速率达到 100~150/s 以上 | 保留第一阶段优化，继续扩大 VUS=200 验证 |
| 消费速率仍接近 72/s | 第一阶段收益不足，直接制作第二阶段原型 |

第一阶段的目标不是承诺 1000 条/秒，而是确认当前事务模型还能挤出多少单库能力，并把锁来源从“猜测”变成可定位证据。

## 4. 第二阶段：库存事件明细 + 批量合并

这是解决逐单库存热行的主要方案。

### 4.1 目标链路

```text
HTTP 请求
  -> Redis Lua 原子预扣和幂等控制
  -> ticket_order + Outbox
  -> RabbitMQ
  -> 消费者事务：订单状态 + 库存事件明细
  -> Ack
  -> Batch 聚合库存事件
  -> 批量更新 ticket_tier_bucket/rush_campaign_bucket
  -> Redis/MySQL/事件明细对账
```

Redis 仍然负责高并发入口闸门，但不再单独承担最终正确性。MySQL 中的库存事件和订单状态负责持久化，库存桶表变成可重建的汇总表。

### 4.2 建议的数据模型

第一版可以新增一张事件表，先不删除现有库存桶表：

```sql
CREATE TABLE inventory_event (
    id BIGINT PRIMARY KEY,
    event_key VARCHAR(128) NOT NULL,
    order_id BIGINT NOT NULL,
    campaign_id BIGINT NULL,
    tier_id BIGINT NOT NULL,
    bucket_no INT NOT NULL,
    event_type VARCHAR(16) NOT NULL,
    quantity INT NOT NULL,
    applied_batch_id BIGINT NULL,
    create_time DATETIME(3) NOT NULL,
    update_time DATETIME(3) NOT NULL,
    UNIQUE KEY uk_inventory_event_key (event_key),
    KEY idx_inventory_event_batch (applied_batch_id, id),
    KEY idx_inventory_event_bucket (tier_id, bucket_no, id),
    KEY idx_inventory_event_order (order_id)
);
```

事件类型建议：

```text
RESERVE  预占库存，数量为正
RELEASE  超时/取消释放，数量为负
REFUND   退款恢复，数量为负
ADJUST   对账人工或系统修正
```

`event_key` 必须由业务操作唯一决定，例如：

```text
reserve:{order_id}
release:{order_id}
refund:{order_id}:{refund_id}
```

这样 RabbitMQ 重投递、消费者重试不会重复产生库存事件。

### 4.3 消费者事务的目标边界

第二阶段消费者事务应变成：

```text
开启事务
  -> 幂等插入 inventory_event
  -> 推进 ticket_order 状态
  -> 必要时写支付超时 Outbox/事件
提交事务
  -> Ack
```

不再在每条订单消息中执行：

```sql
UPDATE rush_campaign_bucket ...
UPDATE ticket_tier_bucket ...
```

如果事件唯一键已存在，消费者必须把它视为幂等成功，而不是再次扣库存。

### 4.4 Batch 合并器

Batch 合并器按事件水位或批次处理：

1. 读取未应用事件；
2. 按 `campaign_id/tier_id/bucket_no` 聚合净变化；
3. 每个桶每批只做一次汇总更新；
4. 写入 `applied_batch_id`；
5. 提交后更新 Redis 对账水位；
6. 失败时重复执行同一批次必须幂等。

批量周期不要直接写死。先测：

```text
10ms、50ms、100ms、500ms、1s
```

比较：

- 消费者速率；
- 订单进入 `pending_payment` 的延迟；
- 汇总库存延迟；
- 锁等待；
- 批量大小；
- Redis 与事件明细的差异窗口。

库存数量对用户入口仍由 Redis 立即控制，因此批量合并不会允许入口无限超卖；它改变的是 MySQL 汇总库存的写入频率。

### 4.5 发布方式

不要直接替换旧逻辑，按以下顺序：

#### 阶段 2A：影子写入

```text
旧桶扣减继续生效
新 inventory_event 同时写入
Batch 只计算，不回写生产汇总
```

对比：

```text
Redis 预扣量
旧库存桶变化
事件净变化
订单状态
超时/取消/退款释放量
```

#### 阶段 2B：双读校验

对外仍读旧汇总，但后台定时比较“旧汇总”和“事件重算结果”。出现差异就进入死信/补偿，不继续扩大压测。

#### 阶段 2C：批量汇总生效

消费者停止逐单更新库存桶，Batch 成为库存桶汇总的唯一写入者；旧逻辑保留回退开关一段时间。

### 4.6 第二阶段验收

必须同时满足：

- 0 超卖；
- 0 丢事件；
- 事件唯一键重复不产生重复扣减；
- 0 未解释的死信；
- Redis、事件明细、库存汇总可对账；
- `rush_campaign_bucket` 锁等待至少显著低于当前基线；
- 消费速率至少达到当前基线的 2 倍，才继续做 VUS=200；
- VUS=100、200 都无 5xx 后，才允许测试 500；
- 500/1000 的结果必须分别报告入口吞吐、消费速率、最终状态和排空时间，不能只报告 HTTP QPS。

如果第二阶段正确性通过但消费速率仍不足，说明单库 Batch 汇总仍是热点，进入第三阶段；如果正确性不通过，回退到旧桶扣减，不继续加压。

## 5. 第三阶段：库存物理分片和水平扩展

只有第二阶段仍无法满足目标时，才做物理分库。简单把一张表拆成多个逻辑表、但仍放在同一个 MySQL 实例上，不算真正分库。

### 5.1 分片键

推荐使用：

```text
shard_key = campaign_id + bucket_no
```

不能只按 `campaign_id` 分片：一个超级热门活动如果全部落到一个库，热点仍然集中。

示意：

```text
Inventory-DB-1: 活动 A 的桶 0~3
Inventory-DB-2: 活动 A 的桶 4~7
Inventory-DB-3: 普通活动和低流量活动
```

### 5.2 链路变化

```text
Redis Lua 预扣
  -> 根据 shard_key 路由 Outbox/MQ
  -> 对应 shard consumer group
  -> 对应 Inventory-DB 的事件明细/汇总
  -> 订单库通过 Outbox/事件推进状态
  -> 跨库对账和补偿
```

订单库和库存库不采用跨库两阶段提交。采用：

- 事件唯一键；
- Outbox；
- 消费幂等；
- 可重放事件；
- 对账和补偿；
- 订单状态机。

### 5.3 第三阶段配套事项

- 分片元数据：活动、票档、桶到数据库的映射；
- 路由缓存和路由版本；
- 分片迁移：暂停活动或双写迁移，禁止直接复制后切换；
- 每个分片独立 MySQL 连接池；
- 每个分片独立消费者组和 prefetch；
- 每个分片独立锁等待、队列、死信和对账指标；
- 分片故障时只影响对应活动或桶；
- Redis Cluster 只有在 Redis 连接、内存或单实例吞吐成为证据瓶颈时才引入，不与 MySQL 分片绑定推进。

### 5.4 第三阶段验收

- 单分片无超卖、无丢事件、无未解释死信；
- 热点分片锁等待不再拖慢其他活动；
- 分片重启后事件可继续消费；
- 分片路由变更可回滚；
- 连接池上限按实例总量计算，而不是每个 Backend 都盲设 100；
- 最终性能以“有效订单确认速率 + 延迟 + 排空时间”验收；
- 目标 1000 条/秒必须明确是入口速率还是最终订单确认速率，二者分别报告。

## 6. 统一压测与证据规范

### 6.1 每轮测试前

```powershell
cd D:\newProgram\gofun
docker compose config
go test ./... -count=1
```

压测必须使用独立 Compose project、独立 MySQL/Redis/RabbitMQ volume，并重新准备活动和用户。k6 使用 Docker 网络地址：

```text
http://backend:8080/api/v1
```

推荐第一阶段复现：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File `
  tests/load/k6/run_full_chain_baseline.ps1 `
  -Project gofun-roadmap `
  -Vus 100,200 `
  -Duration 20s `
  -DrainSeconds 60 `
  -FastPrepare `
  -K6InDocker
```

500/1000 只在 VUS=100、200 无 5xx、无死信、无连接拒绝并且库存对账通过后执行。

### 6.2 必须采集的指标

#### HTTP 入口

```text
http_req_rate
http_req_duration p50/p95/p99
5xx
transport error
业务拒绝
```

#### RabbitMQ

```text
published
consumed{success,retry,permanent_error,dead_letter}
messages_ready
messages_unacknowledged
主订单队列峰值
排空时间
```

#### 订单消费者

```text
ProcessOrderTask transaction avg/p95/p99
成功消费速率 = 成功消费计数器增量 / 观察秒数
retry rate
dead-letter rate
pending_payment 数量

ticket_order_consumer_stage_duration_seconds{stage}
ticket_order_consumer_inventory_bucket_duration_seconds{kind,bucket_no}
ticket_order_consumer_inventory_bucket_operations_total{kind,bucket_no,result}
```

#### MySQL

```text
Threads_connected
Threads_running
Max_used_connections
Connection_errors_max_connections
Innodb_row_lock_current_waits
Innodb_row_lock_waits
Innodb_row_lock_time
具体 data_locks/data_lock_waits
```

#### 一致性

```text
Redis 可用库存
库存事件净变化
库存桶汇总
订单 queued/pending_payment/cancelled
Outbox pending/publishing/published
死信数量
```

### 6.3 结果判定规则

```text
出现 1040                 -> 连接配置问题，本轮性能结果无效
出现 HTTP 5xx              -> 先修业务/数据库错误，本轮不排名
出现死信或库存差异          -> 先修一致性，本轮不排名
MQ 持续增长且 DB 锁高        -> 消费事务/库存模型瓶颈
MQ 持续增长但 DB 锁低        -> publisher、消费者调度或外部依赖瓶颈
HTTP 高但 pending_payment低 -> 只能说明入口接受快，不能说明最终能力
排空窗口到期仍有消息         -> 记录“未排空”，不能把观察上限当真实排空时间
```

## 7. 回滚和安全边界

### 第一阶段

- 保留当前分桶路径；
- 父级状态优化只改写入条件，不改变库存数量语义；
- 出现状态漂移时由补偿任务修正；
- 不在没有幂等替代方案时删除订单 `FOR UPDATE`。

### 第二阶段

- 数据库迁移只新增表和索引，不删除旧桶表；
- 先影子写、再双读校验、最后切换批量汇总；
- 保留 `INVENTORY_LEDGER_ENABLED` 类似的显式开关；
- Batch 失败时暂停切换，不直接清空事件；
- 事件表和 Outbox 未完成对账前，不删除队列消息。

### 第三阶段

- 路由表版本化；
- 新旧分片双读或双写期间保留回退路由；
- 不使用 `git reset --hard`、不删除生产 volume；
- 压测环境可在保存结果和死信快照后清理专用队列/volume，生产队列不能用“直接删除”替代排障。

## 8. 最终执行顺序

```text
当前基线：max_connections=300，6 consumers，消费约72/s
    ↓
第一阶段：SQL/锁细分 + 保守缩短事务
    ↓
如果仍被 rush_campaign_bucket 限制
    ↓
第二阶段：inventory_event + Batch 合并 + 对账
    ↓
如果单库 Batch 仍达不到最终目标
    ↓
第三阶段：按 campaign_id + bucket_no 做物理分片
```

最终项目收敛时，默认配置应只保留经过同一口径验证的组合；`sync`、旧单行库存、旧消费者数量和失败实验保留为回退/对照配置，不作为默认性能结论。
