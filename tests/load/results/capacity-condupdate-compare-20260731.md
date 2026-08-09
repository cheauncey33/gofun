# 消费者条件更新：单热点 1500 档对比（2026-07-31）

## 口径

- 场景：单活动单票档抢票，`STEPS=1500`，`RUNS=3`，`wave=50`，consumer `4×5`，outbox `batch`。
- Before：`capacity-final-c4p5-20260731`（`SELECT FOR UPDATE` + 条件 UPDATE）。
- After：`capacity-after-condupdate-1500-v2-20260731`（去掉票档/活动 `FOR UPDATE`，仅条件 UPDATE；订单行仍 `FOR UPDATE`）。
- After 栈：`gofun-capacity` + 当前代码镜像；采样账号为 `fuchang` / `fuchang-it-*`。
- 两轮不在同一时刻、同一冷热状态，存在 Docker Desktop / 主机噪声；结论以中位数为主。

## 三轮明细

| 组 | 轮次 | 成功 | 入口 QPS | execute p99 | MQ 追平 | row_lock_waits_delta |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Before | 1 | 1500 | 784.75 | 129.34ms | 22.77s | 1359 |
| Before | 2 | 1500 | 775.17 | 92.30ms | 22.92s | 1354 |
| Before | 3 | 1500 | 796.24 | 92.45ms | 22.92s | 1504 |
| After | 1 | 1500 | 714.47 | 165.89ms | 24.06s | 1330 |
| After | 2 | 1500 | 637.65 | 221.77ms | 23.75s | 1504 |
| After | 3 | 1500 | 660.60 | 103.91ms | 28.14s | 2477 |

## 中位数对比

| 指标 | Before | After | 变化 |
| --- | ---: | ---: | ---: |
| 入口 QPS | 784.75 | 660.60 | -15.8% |
| execute p99 | 92.45ms | 165.89ms | +79.4% |
| MQ 追平 | 22.92s | 24.06s | +5.0% |
| row_lock_waits（中位） | 1359 | 1504 | +10.7% |
| row_lock_waits（三轮合计） | 4217 | 5311 | +25.9% |

两组均 1500/1500 成功，`still_queued=0`。

## 解读

1. **条件更新没有改善单热点追平**，MQ drain 仍约 24s（≈62 单/s），与改前同量级。
2. **行锁等待没有下降**：去掉 `SELECT FOR UPDATE` 后，热点行上的 `UPDATE ... WHERE remaining_quota >= ?` 仍会排他；同票档并发消费照样排队。`Innodb_row_lock_waits` 统计的是锁等待次数，不区分 FOR UPDATE / UPDATE。
3. 入口 QPS / p99 变差，更可能来自本机噪声、镜像冷热、ES 等同栈差异，**不能据此断言条件更新拖慢了热路径**；但可以断言：**它不是单热点瓶颈的解药**。
4. 与预期一致：短持锁是工程整洁度/减少多余读锁，冲高单热点吞吐仍需 **分桶** 或弱化 MySQL 实时扣减。

## 证据路径

- Before：`tests/load/results/capacity-final-c4p5-20260731/`
- After：`tests/load/results/capacity-after-condupdate-1500-v2-20260731/`
- 采样修复：`tests/load/lib/resource_sampler.mjs`（默认 `fuchang` / `fuchang-it-*`）
- 拓扑覆盖：`tests/load/docker-compose.capacity.yml`
