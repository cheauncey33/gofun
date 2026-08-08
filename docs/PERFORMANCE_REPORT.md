# Gofun 性能与可靠性验证报告

## 结论边界

本报告只记录 2026-07-23 在本机隔离集成栈实际采集的数据。压测创建了专用活动和用户，没有清空现有数据库。以下数据主要衡量“限时开售入口在大量限购拒绝下的吞吐”，不能解释为持续成功建单能力。

## 环境与固定参数

- Windows 10，Go 1.26.5，Node.js 24.18.0，Docker 29.6.1。
- MySQL 8、Redis 7.4、RabbitMQ 3、Elasticsearch 8.17.1 运行在 Docker。
- 后端运行在宿主机，MySQL/Redis 连接池分别为 20/20。
- 并发 20，持续 10 秒，think time 0；活动库存 100000，每用户限购 6。
- 全局、IP 和分布式写限流在本次容量测试中均显式放宽到 100000，避免把 429 当成容量。
- OTel 采样率 1%，Jaeger OTLP/gRPC 端点 `127.0.0.1:4317`。

## 可复现命令

```powershell
$env:BASE_URL = "http://127.0.0.1:18080/api/v1"
$env:RUSH_TOTAL_QUOTA = "100000"
$env:RUSH_TIER_QUOTA = "100000"
$env:RUSH_PER_USER_LIMIT = "6"
node tests/load/ticket_rush_bootstrap.mjs

$env:RUSH_CAMPAIGN_ID = "<bootstrap 输出>"
$env:CONCURRENCY = "20"
$env:DURATION_SECONDS = "10"
$env:PROFILE_SECONDS = "10"
$env:PPROF_URL = "http://127.0.0.1:16060/debug/pprof"
$env:METRICS_URL = "http://127.0.0.1:18080/metrics"
$env:RUN_LABEL = "before-campaign-cache"
node tests/load/ticket_rush_profile.mjs
```

`ticket_rush_profile.mjs` 同时保存 `load.json`、CPU/heap profile、运行清单以及压测前后的 Prometheus 文本快照。原始结果位于：

- `tests/load/results/before-campaign-cache/`
- `tests/load/results/after-campaign-cache/`

生成交互式火焰图：

```powershell
go tool pprof -http=:9091 tests/load/results/before-campaign-cache/cpu.pprof
go tool pprof -http=:9092 tests/load/results/after-campaign-cache/cpu.pprof
```

## Profile 发现与优化

关闭活动缓存的 profile 中，`RushSaleService.getAvailableCampaign` 累计采样 0.61 秒（总 CPU 样本的 2.74%），每次抢票都会查询同一活动元数据。该数据在活动周期内基本稳定，真正库存仍由 Redis Lua 原子校验，因此增加了可配置的 3 秒本地活动投影缓存，并用 singleflight 合并缓存失效瞬间的回源。`rush_sale.campaign_cache_ttl_ms=0` 可复现优化前行为。

开启缓存后的同条件 profile 中，该函数没有命中 CPU 样本；活动开始/结束时间仍在每次请求中检查。状态变化最多存在一个 TTL 的本地可见延迟。

## Before / After

压测均为 20 并发、10 秒，且均得到 120 次 HTTP 200；其余请求是达到每用户限购后的 HTTP 400 业务拒绝。没有 429、5xx 或网络错误。

| 指标 | Before（缓存关闭） | After（缓存 3 秒） | 变化 |
| --- | ---: | ---: | ---: |
| 请求数 | 12056 | 12425 | +3.1% |
| QPS | 1203.62 | 1241.35 | +3.1% |
| 平均延迟 | 16.60 ms | 16.10 ms | -3.0% |
| p50 | 14.89 ms | 11.83 ms | -20.6% |
| p95 | 26.65 ms | 35.68 ms | +33.9% |
| p99 | 68.79 ms | 90.93 ms | +32.2% |
| 系统错误率 | 0 | 0 | 不变 |

吞吐和中位延迟改善，但单次短测中的 p95/p99 变差，因此不能声称尾延迟得到优化。正式容量结论应至少运行 3 轮 60 秒以上测试并比较中位数。

## 可靠性与运维

### pprof

`pprof.enabled` 默认关闭；开启后 `pprof.host` 必须是 loopback 地址，调试端点不会挂到业务 Gin 端口。生产环境建议只在临时诊断时开启。

### ES 最终一致性

启动全量重建之外，`elasticsearch.sync_interval_minutes` 控制非破坏性周期对账。任务分页 upsert MySQL 已发布活动，枚举 ES ID，对待删除项再次查询 MySQL 状态后删除，最后统一 refresh。Redis 锁 `lock:event-search:sync` 避免多实例重复执行。指标 `event_search_sync_total{result=...}` 区分成功、发现异常、跳过和失败。

### OpenTelemetry / Jaeger

启动 monitoring Compose 后访问 `http://127.0.0.1:16686`，选择配置的 `telemetry.service_name`。一次抢票可按以下 span 查看：

`HTTP POST /rush-sales/:id/execute` → Redis/GORM → `ticket.outbox.enqueue` → `ticket.outbox.publish` → `ticket.order.consume` → `ticket.order.finalize` → `ticket.payment_timeout.publish/consume`

W3C `traceparent`/`tracestate` 同时保存在 outbox payload 和 AMQP headers；重试与 DLQ 复制并更新 headers。默认关闭 tracing，开发环境按比例采样。

### 分布式写限流

核心下单、抢票、评论创建和点赞使用 Redis ZSET + Lua 滑动窗口，以用户 ID 为主键、IP 为回退。Lua 使用 Redis `TIME`，原子完成过期成员清理、计数、写入和 TTL 设置。Redis 故障默认 fail-closed 并返回 503；真实超限返回 429，二者有独立日志和 `distributed_rate_limit_requests_total` 指标。本机 token bucket 仍作为全局/IP 粗筛。

### 毒消息

超时消费者对 `ErrTicketOrderNonRetryable` 立即 Ack 并计入 `permanent_discard`；临时错误 Nack+requeue。DB 扫描兜底复用相同分类函数，指标为 `ticket_timeout_messages_total{result=...}`。

## 已知限制

- 本次机器在更长、50 并发的试跑中出现过 Windows 客户端端口耗尽和系统错误，相关无效试跑未纳入结论。
- 当前有效对比只有一轮 10 秒，尾延迟波动很大。
- 业务拒绝占大多数，成功建单吞吐需用更大的用户池和专门的异步消费积压指标另测。
- 原始 Prometheus 快照已保存；Grafana 仅用于运行时查看，不以截图替代原始数据。
- Grafana：`http://127.0.0.1:3000`（默认 `admin`/`admin`）；Jaeger：`http://127.0.0.1:16686`。

## 验证清单（2026-07-23）

- `cd backend && go test ./... -count=1`：通过
- before/after 压测原始结果：`tests/load/results/before-campaign-cache/`、`tests/load/results/after-campaign-cache/`
- monitoring Compose：Jaeger / Prometheus / Grafana 已拉起；压测结论以 `load.json` 与 pprof 为准

## 补充：3×60s 方差与成功建单路径（同日后续）

为避免 Windows 本机 ephemeral port 耗尽（`connectex: Only one usage of each socket address`），稳定多轮改用 **并发 10 + think_ms 20**。该设置会把 QPS 上界钉在 think-time 附近，因此**不能**用这组参数证明缓存抬吞吐；它只适合比较尾延迟稳定性。

### 3×60s 中位数对比（campaign=11）

| 指标 | Before 中位（关缓存） | After 中位（3s 缓存） | 跨轮极差 Before / After |
| --- | ---: | ---: | --- |
| QPS | 217.04 | 217.75 | 130.43 / 0.54 |
| p50 | 22.46 ms | 21.50 ms | 1.81 / 0.09 |
| p95 | 25.35 ms | 24.75 ms | 3.67 / 0.08 |
| p99 | 28.08 ms | 29.56 ms | 4.13 / 6.89 |

原始汇总：`tests/load/results/variance-before/summary.json`、`variance-after/summary.json`。

解读：在 think-time 受限场景下 QPS 几乎不变；p50/p95 略稳，p99 仍有波动（after 第 3 轮到 34.97ms）。此前单轮 10s「p99 变差」更像噪声，不是缓存拖慢。

### 成功建单 + MQ（200 用户，每人 1 张）

- 活动：campaign=12，quota=5000，`per_user_limit=1`
- 结果：200/200 HTTP 200；轮询后 **200 全部 `pending_payment`**；`still_queued=0`
- 成功入口吞吐：`success_qps=215.02`（execute 墙钟 0.93s）
- MQ：`published_delta=200`，`consumed_success_delta=200`
- 原始：`tests/load/results/success-path-200/load.json`

### 新 profile 热点判断（不改代码）

`variance-after/r3` 中 `getAvailableCampaign` 已采不到样本（before r3 仍约 1.37%）。剩余主要成本在 **抢票拒绝路径上的 MySQL `First`（幂等查询）与连接拨号**，以及鉴权/写限流中间件栈。若再优化，应优先考虑「限购拒绝短路少打 MySQL」或提高连接池复用，而不是继续加活动元数据缓存。

## 多协程消费对比（2026-07-24）

同机单 backend 进程，成功建单 500 用户（每人 1 张，wave=40）：

### 优化前（outbox：每 2s × 最多 50 条）

| 配置 | 入口 success_qps | drain_seconds | MQ published/consumed |
| --- | ---: | ---: | ---: |
| worker=2 / prefetch=2 | 192.72 | （未记 drain；poll≈2888） | 500 / 500 |
| worker=12 / prefetch=10 | 196.25 | **17.93** | 500 / 500 |

### 优化后（写入即唤醒 + 200ms 兜底 + batch=200）

| 配置 | 入口 success_qps | drain_seconds | consume_qps（粗算） | MQ |
| --- | ---: | ---: | ---: | ---: |
| worker=2 / prefetch=2 | 190.84 | **11.59** | 43.14 | 500/500 |
| worker=12 / prefetch=10 | 200.34 | **11.89** | 42.05 | 500/500 |

原始：`tests/load/results/outbox-fix-workers-2/`、`outbox-fix-workers-12/`（及优化前 `workers-*-success-500/`）。

**结论：**

1. Outbox 唤醒后，追平时间从约 **18s → ~12s**，说明原先 2s×50 的节流确实是主因。
2. worker 2→12 仍然几乎打不开差距：发布侧仍是 **单协程串行** `publishPersistent`（含 confirm），喂 MQ 的速率成了新天花板，消费者加再多也经常在等消息。
3. 若要让多 worker 真正吃饱，下一步应在 outbox 批次内有限并发发布（或拆独立 publisher），而不是只加 `worker_count`。

代码改动：`TicketOrderService.outboxNotify` + `drainPendingOutbox`；`ticketOutboxPublishBatch=200`，`ticketOutboxTickInterval=200ms`。

## 多 Publisher（2026-07-25）

实现：`order_outbox.publish_workers` 个投递协程，各开独立 amqp channel + confirm；用 `SELECT … FOR UPDATE SKIP LOCKED` 把 `pending` 抢成 `publishing`，发成功标 `published`，失败退回 `pending`（超限 `failed`）。启动时把残留 `publishing` 复位。配置见 `config.example.yaml`。

固定 `publish_workers=4`，500 成功单复测：

| consumer worker | drain_seconds | success_qps | MQ |
| --- | ---: | ---: | ---: |
| 2 | **9.72** | 160.27 | 500/500 |
| 12 | **9.74** | 105.33 | 500/500 |

对照：单 publisher 唤醒版约为 **11.6–11.9s** drain。多 publisher 把追平再压低约 **2s**，但 **consumer 2→12 仍几乎打不开**——说明 500 单突发下，喂 MQ / 落库已能被 2 个 consumer 吃掉，或剩余瓶颈在单 channel 内串行 confirm、抢行与本机 DB 池，而不是 consumer 数量。

原始：`tests/load/results/multipub-workers-2/`、`multipub-workers-12/`。

## 成功热路径减负（2026-07-25）

改动要点：

1. **订单 + outbox 同事务**（`createOrderAndOutbox`）
2. **Redis 幂等短路**（命中直接返回，避免重复 `First`）
3. **票档可售上下文本地缓存 3s**（少打 4 次 MySQL）
4. **普通下单改为先 Redis 预扣再落库**，避免无效订单行
5. **MySQL 连接池** 集成配置 `20/80`
6. **Outbox 批量 deferred confirm**（同 channel 先连发再等 ack）
7. 观演人仅在有数据时写入；展示快照仍在热路径写入（延后到 consumer 补齐会拖慢 drain）

500 成功单对比（同机，`publish_workers=4`，consumer=12）：

| 版本 | success_qps | execute_s | drain_s | 入口 p50 |
| --- | ---: | ---: | ---: | ---: |
| 多 publisher 基线 | ~196–200 | ~2.5 | ~9.7 | ~150ms |
| 热路径减负 v2 | **278** | **1.8** | 14.1 | ~120ms 量级 |

原始：`backend/tests/load/results/hotpath-v2-success-500/`（脚本在 `backend/` 目录启动时结果写到该相对路径）。

**结论：** 入口成功吞吐大约 **+40%**（~200 → ~278）。drain 略回升，优先保证入口；若要再压 drain，应继续看 consumer 事务与发布侧，而不是把快照补齐塞回消费热路径。

## Outbox write_mode：sync vs batch（2026-07-25）

配置：`order_outbox.write_mode=sync|batch`（默认 sync）。batch 模式：

- 热路径只同步写订单；outbox 进有界内存缓冲
- Flusher：满 `buffer_batch_size`（50）或每 `flush_interval_ms`（8ms）批量 `CreateInBatches`
- 缓冲满则调用方同步刷盘腾位；刷失败回填缓冲并 sync 兜底
- `RecoverQueuedOrders` 周期扫描（默认 2s）补「queued 且无 active outbox」
- 指标：`outbox_buffer_len`、`outbox_flush_batch_size`、`outbox_recover_backfill_total`

压测（consumer=12，publish_workers=4）：

| 模式 | 用户数 | success_qps | execute_s | drain_s | 最终 pending_payment |
| --- | ---: | ---: | ---: | ---: | ---: |
| sync | 500 | 326.51 | 1.53 | 11.04 | 500/500 |
| batch | 500 | **485** | **1.03** | 10.59 | 500/500 |
| batch | 1000 | **649** | 1.54 | 22.25 | 1000/1000 |

原始：`tests/load/results/outbox-sync-500/`、`outbox-batch-500/`、`outbox-batch-1000/`。

**结论：** batch 在 500 单上把入口成功吞吐从 ~327 提到 **~485（约 +48%）**；1000 单突发约 **649 QPS**，且 MQ 追平后无残留 queued。默认仍建议生产用 sync；压测/开售演练可开 batch。

## 最终收敛版本（锁定）

本轮性能改造收敛为下面这一套，不再继续加新优化点：

| 能力 | 收敛取值 |
| --- | --- |
| 活动元数据缓存 | `rush_sale.campaign_cache_ttl_ms=3000` |
| Outbox 写入 | **`write_mode=batch`**（可用 `sync` 回退） |
| Outbox 缓冲 | batch 50 / flush 8ms / max 4000 |
| Outbox 发布 | publish_workers=4，publish_batch=200，批量 confirm |
| 周期补单 | recover_interval_sec=2 |
| 成功热路径 | Redis 幂等短路 + 票档上下文短缓存 + 订单/outbox 路径如上 |
| 连接池（集成） | max_open_conns=80 |

成功路径参考数字（本机隔离栈）：

- sync 500：~327 QPS  
- **batch 500：~485 QPS**  
- **batch 1000：~649 QPS**（全部 `pending_payment`）

`sync` 仍保留为开关，不删代码；仓库示例与集成配置默认走 **batch**。
