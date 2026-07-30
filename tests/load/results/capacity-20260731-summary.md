# 抢票链路单机容量扫描（2026-07-31）

## 范围与口径

- 拓扑：单 Go 后端、单 MySQL、单 Redis、单 RabbitMQ，均运行在同一台 Windows Docker Desktop 主机。
- 模式：`order_outbox.write_mode=batch`。
- 基线参数：Consumer 4、prefetch 5、Publisher 4、publish batch 200、MySQL 最大连接 100、Redis 连接池 100。
- 流量：每个用户仅成功购买 1 张票；每档使用独立活动；wave size 50。
- 阶梯：250 / 500 / 750 / 1000 / 1500 单，每档 3 轮。
- 指标采样仅覆盖抢票 execute 和 MQ 追平阶段，不包含用户注册与登录准备。
- QPS 为成功建单入口实测点，不代表系统极限或生产容量。

## sync Outbox 死锁修复

修复前，sync 模式 1000 单突发出现 85 个 MySQL 1213 死锁，只成功 915 单。

修复内容：

1. Outbox Publisher 抢取事务改用 `READ COMMITTED`，避免默认 `REPEATABLE READ` 范围锁与新 Outbox 插入竞争。
2. sync 下单事务对 MySQL 1213 / 1205 增加最多 3 次有限退避重试。

修复后验收：

- 1000 / 1000 成功。
- 入口 680.75 QPS。
- p99 154.06ms。
- MQ 15.98s 追平。
- 日志中 1213、事务重试耗尽和 HTTP 500 均为 0。

## batch 基线阶梯结果

以下均为三轮中位数；括号内是三轮范围。

| 单量 | 成功 | QPS | p99 | MQ 追平 |
| ---: | ---: | ---: | ---: | ---: |
| 250 | 750/750 | 671.43（598.33–718.83） | 84.04ms（82.96–120.60） | 4.45s |
| 500 | 1500/1500 | 579.37（481.95–622.15） | 231.54ms（105.08–291.09） | 8.31s |
| 750 | 2250/2250 | 698.87（584.81–760.45） | 92.11ms（75.96–118.58） | 12.12s |
| 1000 | 3000/3000 | 746.95（714.96–815.32） | 84.48ms（71.04–95.45） | 15.86s |
| 1500 | 4500/4500 | 768.52（710.16–795.43） | 119.22ms（101.54–172.37） | 23.37s |

1500 单仍为 100% 成功，因此本次没有找到系统失败上限。稳定结论只能写成“已验证到 1500 单突发”，不能写成“最多支持 1500 单”。

## 资源与瓶颈

基线 1500 单三轮的观测峰值：

- 后端 CPU：79.68%。
- MySQL CPU：115.74%（Docker 百分比可超过单核 100%）。
- MySQL Threads_running：60。
- MySQL deadlocks：0。
- MySQL row lock waits：三轮累计 4340。
- Redis：最高约 10178 ops/s，采样延迟最高 1ms，无拒绝连接。
- RabbitMQ 订单队列 ready：最高 1149。
- RabbitMQ unacked：最高 20，等于 4 Consumer × prefetch 5。

MQ 追平时间随单量近似线性增长，约 64 单/s。Redis 没有表现为瓶颈；MySQL 热点票档/活动行锁和消费事务是主要约束。

## 参数 A/B

候选参数：Consumer 12、prefetch 10；Publisher、MySQL 和 Redis 连接池保持不变。

| 配置 / 1500 单 | 成功 | QPS 中位数 | p99 中位数 | MQ 追平中位数 | 后端 CPU 峰值 | MySQL CPU 峰值 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| 4 × 5 基线 | 4500/4500 | 768.52 | 119.22ms | 23.37s | 79.68% | 115.74% |
| 12 × 10 | 4500/4500 | 629.86 | 153.81ms | 23.22s | 110.92% | 125.32% |
| 4 × 5 回退复测 | 4500/4500 | 784.75 | 92.45ms | 22.92s | 75.50% | 79.50% |

12 × 10 仅将追平中位数改善 0.15s，却显著恶化入口 QPS、p99 和资源占用。增加消费者不能绕过同一票档/活动热点行的串行锁约束。

## 最终参数

```yaml
mysql:
  max_idle_conns: 10
  max_open_conns: 100

redis:
  pool_size: 100

order_consumer:
  worker_count: 4
  prefetch_count: 5

order_outbox:
  write_mode: batch
  publish_workers: 4
  publish_batch: 200
  buffer_batch_size: 50
  flush_interval_ms: 8
  max_buffer: 4000
```

Publisher 已快于 Consumer，继续增加 Publisher 只会扩大 RabbitMQ 积压；MySQL 活跃线程未打满 100 连接上限；Redis 也没有池耗尽或延迟证据，因此保持原连接池。

## 证据路径

- `capacity-baseline-batch-20260731/staircase-summary.json`
- `capacity-tuned-c12p10-20260731/staircase-summary.json`
- `capacity-final-c4p5-20260731/staircase-summary.json`
- 每轮目录下的 `load.json`、`resources.json`、`metrics-before.prom`、`metrics-after.prom`
