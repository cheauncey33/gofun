# 配置组合矩阵与项目收敛

本文把“配置支持”“曾经跑过”“当前建议”分开。历史结果来自 `tests/load/results/`；新的可重复矩阵入口是 `tests/load/run_capacity_matrix.ps1`。

## 当前可收敛的主组合

> 本文矩阵记录的是重构前历史样本。当前代码已删除 Outbox batch 写入和库存 Batch Saga；后续压测只比较库存是否分桶，消费者和连接池按固定基线执行。

先比较真正影响票务热路径的变量：库存是否分桶。消费者和连接池固定为容量栈的基线，避免一次改变太多变量。

| 组合 | 库存 | Outbox | 当前证据 | 结论 |
|---|---|---|---|---|
| `baseline-sync` | 单行/父表热路径 | `sync` | `outbox-sync-*`、`rerun-sync-*` | 一致性路径最直观，吞吐较低，保留为回退档 |
| `baseline-batch` | 单行/父表热路径 | `batch` | `capacity-baseline-*` | 比 sync 更适合入口突发，但仍受单行 MySQL 热点限制 |
| `buckets-sync` | 8 桶 | `sync` | 本文 2026-08-09 矩阵 | 用于隔离“分桶本身”的效果，不作为默认组合 |
| `buckets-batch` | 8 桶，父表退出消费热路径 | `batch` | 本文 2026-08-09 矩阵、`capacity-buckets-8-noparent-*`、k6 峰值 | 当前推荐的单机消费容量档 |

### 2026-08-09 同环境矩阵结果

运行环境是单 Backend、单 Redis、单 MySQL、单 RabbitMQ；每个组合独立重建容器和数据卷，测试 500/1000 两档，每档 2 次。表中是 4 次有效运行的中位数；有效条件为 `success_rate=1` 且 `still_queued=0`。

| 组合 | 有效轮次 | 成功建单 QPS 中位 | 入口 p99 中位 | MQ 追平中位 | MySQL `row_lock_waits` 中位 |
|---|---:|---:|---:|---:|---:|
| `baseline-sync` | 4/4 | 608.96 | 133.48ms | 11.82s | 670.5 |
| `baseline-batch` | 4/4 | **680.91** | **91.89ms** | 12.11s | 751.5 |
| `buckets-sync` | 4/4 | 436.33 | 226.86ms | 5.25s | **117.5** |
| `buckets-batch` | 4/4 | 564.29 | 113.40ms | **4.21s** | 228.5 |

这组结果给出的不是一个“所有指标都第一”的组合：

- 如果只看入口成功建单吞吐，`baseline-batch` 最高；它仍把消费侧共享父行留在热路径，行锁等待最高。
- 如果看异步订单最终落库的追平速度和锁竞争，`buckets-batch` 更合适：MQ 追平最快，行锁等待显著低于两个 baseline。
- `buckets-sync` 的行锁等待最低，但入口吞吐和 p99 不如 `buckets-batch`，更适合作为分桶一致性对照。
- 四个组合的 500/1000 档均为全量成功、无残留排队；这证明的是本地测试拓扑下的正确性和相对差异，不是生产容量承诺。

本次结果文件：`tests/load/results/capacity-matrix-20260809-182908/matrix-summary.md`，原始运行目录在同级 `capacity-matrix-<case>-<timestamp>/`。

## 其他参数：验证过什么，没验证什么

| 参数/组件 | 当前验证范围 | 是否纳入性能排名 |
|---|---|---|
| 库存分桶 | 单行基线、分桶 v1、父表退出热路径的分桶 v2；本次又补了 `sync/batch` 交叉矩阵 | 是，作为主收敛变量 |
| Outbox `sync/batch` | 历史单量测试、本次四组合矩阵；batch 的补写和恢复路径已有代码/测试覆盖 | 是，作为主收敛变量 |
| consumer worker / prefetch | 有 worker=2/12 的历史对比；本次固定 6/5，避免混入第三个变量 | 否，当前只固定容量基线 |
| publisher worker / batch | 有 publisher=4 的历史和本次固定 4/200 的矩阵；没有做完整的 publisher 规模扫描 | 否，先固定 4/200 |
| 单 Redis | 单机容量栈直连 Redis，作为吞吐基线 | 是，单机档 |
| Redis Sentinel | 一主一从 + 3 Sentinel，验证主库故障后的选主和重新发现 | 否，属于高可用/故障演练档 |
| Redis Cluster / 多主写入 | 当前代码和测试没有覆盖 | 不支持宣称 |
| RabbitMQ classic / quorum | classic 用于单机容量；quorum 在集群故障演练中验证消息可靠性 | 不混排，拓扑分开记录 |
| MySQL / Redis 连接池 | 矩阵固定为 MySQL 100/10、Redis 100，未做连接池大小扫描 | 否，生产值需按实例重测 |
| 限流分布式写入、缓存 TTL、重试参数 | 有配置和局部路径验证，但未形成完整的多维性能矩阵 | 否，保留为功能/故障参数 |

所以本轮收敛不是把所有配置排列组合穷举，而是先锁定对热路径影响最大的 2×2；其余参数按“容量基线、可用性拓扑、故障回退”分层，避免产生无法解释的组合数字。

## 已有实测证据

### 分桶必须配合父表退出热路径

在单活动单票档、1500 单、3 轮、wave=50、consumer 4×5、Outbox batch 下：

| 方案 | 入口 QPS 中位 | MQ 追平中位 | `row_lock_waits` 中位 | 正确性 |
|---|---:|---:|---:|---|
| 单行基线 | 784.75 | 22.92s | 1359 | 1500/1500，无超卖 |
| 分桶 v1，但仍每单回写父表 | 623.16 | 27.55s | 2064 | 1500/1500，无超卖 |
| 分桶 v2，父表退出热路径 | 581.18 | 11.41s | 274 | 1500/1500，无超卖 |

因此不能只说“分桶后 QPS 变高”：本机入口 QPS 受噪声影响且不是本改造目标；能确认的是消费侧行锁等待约降 80%，MQ 追平约减半。

证据：`tests/load/results/capacity-buckets-compare-20260731.md`。

### batch 主要改善入口成功建单吞吐

历史 500/1000 成功建单结果（consumer=12、publisher=4）显示：

| 模式 | 单量 | 成功建单 QPS | MQ 追平 | 最终状态 |
|---|---:|---:|---:|---|
| sync | 500 | 326.51 | 11.04s | 500/500 `pending_payment` |
| batch | 500 | 485.00 | 10.59s | 500/500 `pending_payment` |
| batch | 1000 | 649.00 | 22.25s | 1000/1000 `pending_payment` |

历史 batch 的代价是订单和 Outbox 不在同一个本地事务中；历史版本曾用有界内存缓冲、失败回退和 `RecoverQueuedOrders` 扫描补写来收敛风险。当前实现已删除这套路径，订单与 Outbox 固定同事务提交。

证据：`docs/PERFORMANCE_REPORT.md`、`tests/load/results/outbox-sync-500/`、`tests/load/results/outbox-batch-500/`、`tests/load/results/outbox-batch-1000/`。

### consumer worker / prefetch 不是越大越好

历史结果中 worker=2 与 worker=12 的 500 单成功路径，入口吞吐接近，MQ 追平改善有限；更高的 prefetch 只会提高 unacked，在 MySQL 热行无法并行时会增加排队和锁竞争。本次矩阵固定 consumer=6、prefetch=5，暂不把它们作为继续扩大的优化方向。

### 单 Redis、多 Redis 与高可用要分开说

- 单机容量栈：单 Redis 直连，适合测吞吐。
- 集群栈：Redis 一主一从 + 3 Sentinel，实际验证过主库停止后的选主和应用重新发现；这不是 Redis Cluster，也不是多主写入。
- 集群栈还包含 RabbitMQ quorum、3 个 Backend、MySQL 主从复制。它验证的是故障与拓扑，不应与单机 Docker 的吞吐数字直接横比。
- 当前应用仍只写 MySQL 主库，没有 MySQL 自动主库提升或读写切换。

证据：`tests/load/results/cluster-20260731-summary.md`、`deploy/cluster/README.md`。

## 当前项目收敛建议

| 范围 | 收敛决定 |
|---|---|
| 正式单机基线/开售档 | `inventory.buckets_enabled=true`、`bucket_count=32`、Consumer=6、Prefetch=5、Publisher=4；存量数据启动时幂等补桶并预热 Redis |
| Outbox | `write_mode=batch`、buffer=50、flush=8ms、publisher=4、publish_batch=200 |
| 消费者 | 单机容量基线先用 worker=6、prefetch=5；不再以盲目加 worker 作为优化方向 |
| 数据库/Redis | 单机容量测试使用 MySQL max open=100、Redis pool=100；生产值需按实例规格重新压测 |
| Redis 高可用 | Sentinel 作为可用性部署档，不纳入单机性能排行榜 |
| RabbitMQ | classic 用于单机吞吐；quorum 用于集群可靠性演练，二者指标分开记录 |
| 实验开关 | 保留 `sync`、单行库存、缓存 TTL=0 作为对照/回退，不再作为产品默认路径 |

“最好”不是单一答案：若指标是入口成功建单吞吐，优先 `baseline-batch`；若指标是异步订单落库、MQ 追平和行锁竞争，收敛到 `buckets-batch`；若指标是最直观的本地事务边界，选 `sync`；若指标是故障切换能力，选 Sentinel/quorum 集群档，但它的性能数字不能和单机档混报。

## 复现矩阵

```powershell
cd D:\newProgram\gofun
powershell.exe -ExecutionPolicy Bypass -File tests/load/run_capacity_matrix.ps1 `
  -Cases baseline-sync,baseline-batch,buckets-sync,buckets-batch `
  -Steps 500,1000 `
  -Runs 2
```

脚本会：

1. 使用独立 `gofun-matrix` Compose 项目和 18180 等端口；
2. 每个组合重建专用测试卷，避免旧订单、延迟队列和索引污染横向比较；
3. 执行同一套 `ticket_rush_staircase.mjs`；
4. 输出每个组合的成功建单 QPS、入口 p99、MQ 追平、MySQL 行锁等待和合法轮次。

默认结束时会删除 `gofun-matrix` 测试容器和卷；如需保留现场，加 `-KeepStack`。不要把该命令用于开发/生产 Compose 项目。
