# 票务集群拓扑与容量验证（2026-07-31）

## 验证范围

- 同一台 Windows Docker Desktop 主机，不代表跨主机生产容量。
- Nginx 轮询三个 Go Backend，Snowflake 节点号分别为 11、12、13。
- Redis 一主一从、三个 Sentinel；应用通过 `NewFailoverClient` 发现主库。
- RabbitMQ 三节点，经 HAProxy 接入；订单、重试、死信、延时和超时队列均为三副本 quorum。
- MySQL 8.0 GTID 主从；业务只写主库，从库保持 `read_only` 和 `super_read_only`。
- 每个 Backend：Consumer 2、prefetch 5、Publisher 2、MySQL 连接上限 50、Redis 连接池 50。
- 阶梯 250 / 500 / 750 / 1000 / 1500，每档三轮；用户准备不计入入口吞吐。

## 阶梯结果

以下为三轮中位数，所有 11250 个建单请求均成功。

| 单量 | 成功 | 入口 QPS | P99 | MQ 追平 |
| ---: | ---: | ---: | ---: | ---: |
| 250 | 750/750 | 562.12 | 91.58ms | 6.60s |
| 500 | 1500/1500 | 564.21 | 132.59ms | 12.80s |
| 750 | 2250/2250 | 567.00 | 142.63ms | 18.35s |
| 1000 | 3000/3000 | 581.10 | 147.19ms | 24.22s |
| 1500 | 4500/4500 | 563.63 | 171.72ms | 36.64s |

1500 档三轮范围：

- QPS：535.28–596.24。
- P99：133.91–324.38ms。
- MQ 追平：36.61–36.72s。

本次仍未测出失败上限，只能表述为“集群拓扑已验证 1500 单突发”。

## 1500 档资源峰值

- 三个 Backend CPU：48.22% / 45.33% / 46.91%。
- MySQL 主库 CPU 111.46%，从库 CPU 116.55%，`Threads_running` 最大 67。
- 三轮 MySQL 行锁等待增量合计 4468，死锁增量 0。
- Redis 主库 CPU 13.72%，采样延迟峰值 13ms，无拒绝连接。
- RabbitMQ 三节点 CPU 峰值 293.46% / 137.77% / 178.76%。
- 订单队列 ready 峰值 1319，unacked 峰值 30，等于 3 × 2 Consumer × prefetch 5。

多 Backend 分散了应用 CPU，但没有突破单主 MySQL 热点行锁；quorum 的三副本确认显著增加 RabbitMQ CPU 和追平时间。因此同机集群的 1500 档入口数据低于单节点最终复测的 784.75 QPS，不能据此宣称“扩容后吞吐提升”。

## 故障验证

### Nginx / Backend

- 30 个并发健康请求分布为 9 / 11 / 10。
- 停止一个 Backend 后，20 个并发请求全部由剩余两个实例返回 200。

### Redis Sentinel

- 停止 Redis 主库后，三个 Sentinel 将从库提升为主库。
- 切换期间应用重新发现主库，管理员登录返回 200。
- 恢复旧主库后成为从库；验证结束执行受控 failback，恢复初始主从角色。

### RabbitMQ

- 停止 quorum leader 所在节点后，其余两个节点保持全部队列在线。
- 修复应用连接重拨和 Outbox 批次释放后，故障状态下 100 单全部成功：
  - 554.19 QPS。
  - P99 90.91ms。
  - 3.30s 追平。
- 节点恢复后，五个队列均恢复 3/3 在线副本。

### MySQL

- `Replica_IO_Running=Yes`。
- `Replica_SQL_Running=Yes`。
- `Seconds_Behind_Source=0`。
- 主从均存在相同迁移和业务数据。

当前只实现主从复制，没有自动主库提升、VIP/ProxySQL 切换或应用写库故障转移，不应描述为 MySQL 自动高可用。

## 索引实验

- Outbox 抢占已使用 `(status, create_time)` 覆盖索引，无需调整。
- 用户订单列表每用户 15 行，实测约 0.09ms，不新增排序索引。
- 尝试为支付超时扫描增加 `(status, delete_time, expires_at)`：
  - 优化器仍选择原 `delete_time` 索引。
  - 强制索引仅由约 0.17ms 降至 0.09ms。
  - 该查询不是当前瓶颈，额外索引会增加建单写放大，因此已回滚。

## 证据

- `cluster-baseline-20260731/staircase-summary.json`
- `cluster-rabbit-failover-fixed-20260731/staircase-summary.json`
- 每轮目录中的 `load.json`、`resources.json`、`metrics-before.prom`、`metrics-after.prom`
