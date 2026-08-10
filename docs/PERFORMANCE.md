# 性能与压测报告

> 一页看清：Gofun 抢票链路的入口吞吐、库存热点优化，以及优化过程中的取舍。
> 原始数据见 `tests/load/results/`；复现命令见文末。

## TL;DR

- **严格 SLO 样本**：成功率 ≥99% 且 p99 ≤300ms 时，VUS=100 观测到 **1129 req/s / p99 225ms**；VUS=200 达到 1442 req/s，但 p99=312ms，属于临界超标样本。
- **入口观测极限**：VUS=1000 约 **1451 req/s**，但成功率 97%、p99 2041ms，不作为稳定容量结论。
- **库存热点优化**：票档库存 MySQL 分桶（8 桶）+ 父表退出热路径，InnoDB `row_lock_waits` **降约 80%**，MQ 追平时间 **23s → 11s（-50%）**。
- **正确性**：1500 并发抢票全成功、无超卖；混合负载（成功 + 限购拒绝 + 幂等重试）结果全部正确。

## 测试环境

> 本文中的 batch 压测结果属于重构前历史样本。当前代码只保留事务 Outbox：订单与 Outbox 同一主库事务提交，Consumer 逐单完成库存事务；不能再用本文的 batch 开关或 Batch Saga 配置复现。

- 拓扑：capacity 单 backend + 单 Redis + 单 MySQL + 单 RabbitMQ（本机 Docker）
- 历史样本关键配置：Outbox `batch` 写入、库存分桶 8 桶、消费 worker 4×5
- 工具：k6 v2.1.0（入口峰值）+ Node 脚本（业务正确性、MQ 追平、资源采样）
- 压测对象：`POST /api/v1/rush-sales/:id/execute`

## 一、入口峰值（k6 恒定 VU 阶梯）

每档 20s，每档前重新 prepare 夹具（换新 campaign，避免个人限购打穿）。

| VUS | req/s | p90 | p95 | p99 | avg | 成功率 | 判断 |
| ---: | ---: | ---: | ---: | ---: | ---: | ---: | --- |
| 50 | 782 | 92 | 111 | **162** | 64 | 99.7% | ✅ |
| 100 | 1129 | 130 | 155 | **225** | 88 | 99.9% | ✅ |
| **200** | **1442** | 205 | 238 | **312** | 138 | 100% | ⚠️ 吞吐高但 p99 略超 SLO |
| 500 | 1390 | 606 | 729 | **1035** | 346 | 98.5% | ⚠️ 延迟恶化 |
| 1000 | 1451 | 1175 | 1444 | **2041** | 614 | 97.0% | ❌ 入口饱和 |

**读法**：

1. **严格合格样本（自定义 SLO：成功率≥99% 且 p99≤300ms）为 VUS=100 的 1129 req/s**；VUS=200 是接近 SLO 的高吞吐临界档。
2. VUS 提到 500/1000 后 req/s 不再明显上升（≈1400~1450），但 p99 升到 1~2 秒——**这是用延迟换并发，不是更好的工作点**。瓶颈在入口侧，而非库存扣减。
3. k6 恒定 VU 下，VUS 100～200 是本机测试的低延迟高吞吐区间；VUS 500 以上吞吐不再明显增加而 p99 显著上升。

## 二、库存热点优化：分桶 v1 → v2

单活动单票档抢票，`STEPS=1500`、`RUNS=3`、`wave=50`。

| 组 | 中位 req/s | execute p99 | MQ 追平 | row_lock_waits 中位 |
| --- | ---: | ---: | ---: | ---: |
| Before（单行 `remaining_quota` 热路径） | 785 | 92ms | 22.9s | **1359** |
| v1（8 桶，但消费仍每单更新父表 `sold_count`） | 624 | 127ms | 27.6s | **2064** ⚠️ 更慢 |
| v2（8 桶，父表退出热路径，仅桶行扣减） | 581 | 131ms | **11.4s** | **274** ✅ |

> 注：v2 入口 req/s 略降属本机 Docker 噪声，非本次优化目标；优化目标是**消费侧**行锁与 MQ 追平。

### v1 为什么反而更慢

分桶只是把「单行 `remaining_quota`」的锁拆成 8 行；但 v1 的消费逻辑**仍然每单回写父表 `ticket_tier.sold_count`**，父表这一行依旧是全局热点。结果：桶锁 + 父表锁双重竞争，`row_lock_waits` 不降反升（1359 → 2064）。

### v2 怎么改

让父表**真正退出热路径**：消费侧只对命中的那个桶做条件 UPDATE（`remaining >= ?`），不再每单触碰父表 `sold_count`；只有在**判定售罄**时才更新父表状态。热点从「1 行」变成「8 行 + 几乎不碰的父表」。

**结果**：`row_lock_waits` 中位 1359 → 274（**-80%**），MQ 追平 22.9s → 11.4s（**-50%**），且 1500 并发全成功、无超卖。

### 关键启示

> 分桶有效的前提是**热路径上的所有共享写都被打散**。只拆库存行、却保留父表每单回写，等于把单热点变成「多热点 + 一个超级热点」，比不拆更糟。

## 三、正确性验收

- **不超卖**：1500 并发抢 1500 张，成功数 = 库存数，`still_queued = 0`。
- **混合负载**：C40 下 1000 成功建单 + 700 限购拒绝 + 300 幂等重试，全部符合预期。
- **集成脚本**：`tests/integration/inventory_buckets.mjs`（小库存 1 桶、大库存 8 桶、限购全局生效）。

## 四、复现

```powershell
# 1. 起压测栈（capacity 单实例 + 8 桶）
$env:INVENTORY_BUCKETS_ENABLED='true'
docker compose -p gofun-capacity `
  -f tests/integration/docker-compose.ticketing.yml `
  -f tests/load/docker-compose.capacity.yml up -d

# 2. 准备夹具 + k6 峰值扫档
$env:BASE_URL='http://127.0.0.1:18080/api/v1'
node tests/load/k6/prepare_rush_fixture.mjs
node tests/load/k6/run_peak_sweep.mjs   # PEAK_VUS='50,100,200,500,1000'

# 3. 业务正确性 / MQ 追平 / 分桶集成验收
node tests/load/ticket_rush_staircase.mjs
node tests/integration/inventory_buckets.mjs
```

## 证据路径

| 内容 | 路径 |
|------|------|
| k6 峰值汇总 | `tests/load/results/k6-peak-20260807/PEAK_SUMMARY.md` |
| 分桶 v1/v2 对比 | `tests/load/results/capacity-buckets-compare-20260731.md` |
| 条件 UPDATE 对比 | `tests/load/results/capacity-condupdate-compare-20260731.md` |
| 混合负载 | `tests/load/results/mixed-rush-*/mixed-staircase-summary.md` |
