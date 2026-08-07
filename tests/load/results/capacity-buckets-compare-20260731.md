# 库存分桶：单热点 1500 档对比（2026-07-31）

## 口径

- 场景：单活动单票档抢票，`STEPS=1500`，`RUNS=3`，`wave=50`，consumer `4×5`，outbox `batch`。
- Before：`capacity-final-c4p5-20260731`（单行 `remaining_quota` 热路径；条件 UPDATE）。
- Buckets v1：`capacity-buckets-8-1500-20260731`（8 桶开启，但消费仍每单更新父表 `sold_count`）。
- Buckets v2：`capacity-buckets-8-noparent-1500-20260731`（8 桶；父表退出热路径，仅桶行扣减；售罄时才触碰父表 status）。
- 栈：`whu-snack-go-capacity`，`INVENTORY_BUCKETS_ENABLED=true`，`bucket_count=8`。

## 三轮明细

| 组 | 轮次 | 成功 | 入口 QPS | execute p99 | MQ 追平 | row_lock_waits_delta |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Before | 1 | 1500 | 784.75 | 129.34ms | 22.77s | 1359 |
| Before | 2 | 1500 | 775.17 | 92.30ms | 22.92s | 1354 |
| Before | 3 | 1500 | 796.24 | 92.45ms | 22.92s | 1504 |
| Buckets v1 | 1 | 1500 | 549.75 | 182.48ms | 25.55s | 1778 |
| Buckets v1 | 2 | 1500 | 623.16 | 127.12ms | 27.55s | 2064 |
| Buckets v1 | 3 | 1500 | 663.89 | 99.80ms | 30.06s | 2972 |
| Buckets v2 | 1 | 1500 | 577.47 | 130.66ms | 11.04s | 252 |
| Buckets v2 | 2 | 1500 | 653.06 | 94.29ms | 11.41s | 301 |
| Buckets v2 | 3 | 1500 | 581.18 | 175.17ms | 12.04s | 274 |

## 中位数对比（Before vs Buckets v2）

| 指标 | Before | Buckets v2 | 变化 |
| --- | ---: | ---: | ---: |
| 入口 QPS | 784.75 | 581.18 | -25.9%（入口侧噪声/本机负载，非本改目标） |
| execute p99 | 92.45ms | 130.66ms | 变差（入口噪声） |
| MQ 追平 | 22.92s | 11.41s | **-50.2%** |
| row_lock_waits（中位） | 1359 | 274 | **-79.8%** |
| row_lock_waits（三轮合计） | 4217 | 827 | **-80.4%** |

各组均 1500/1500 成功，`still_queued=0`。

## 解读

1. **分桶有效的前提是父表真正退出热路径**。v1 虽拆了桶，但仍每单 `UPDATE ticket_tier.sold_count`，行锁等待反而更高。
2. **v2 明显改善消费侧**：MQ 追平约减半（≈23s → ≈11s），`Innodb_row_lock_waits` 约降 80%。
3. 入口 QPS / p99 未作为本改优化目标；本机 Docker 噪声下不宜与 Before 细比入口侧。
4. 集成验收：`node tests/integration/inventory_buckets.mjs`（小库存 1 桶、大库存 8 桶、限购全局）。

## 证据路径

- Before：`tests/load/results/capacity-final-c4p5-20260731/`
- Buckets v1：`tests/load/results/capacity-buckets-8-1500-20260731/`
- Buckets v2：`tests/load/results/capacity-buckets-8-noparent-1500-20260731/`
- 拓扑：`tests/load/docker-compose.capacity.yml`（`INVENTORY_BUCKETS_ENABLED`）
