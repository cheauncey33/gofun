# Gofun 项目总结与简历文稿

## 项目概述

Gofun 是一个面向多主办方活动的票务平台，第一阶段聚焦“无选座、多票档、普通购票 + 限时抢票”。核心目标不是只完成 CRUD，而是验证高并发开售时的库存正确性、异步削峰、订单幂等、支付状态边界和故障恢复。

## 可写进简历的版本

### 一句话版

负责 Gofun 多主办方活动票务平台后端设计与性能验证，围绕 Redis Lua 预扣、事务 Outbox、RabbitMQ 异步落单和 MySQL 库存分桶，完成抢票、支付沙箱、电子票核销及一致性补偿链路。

### 项目经历版

**Gofun｜高并发活动票务平台｜Go / Gin / GORM / MySQL / Redis / RabbitMQ / Vue 3**

- 设计普通购票与限时抢票统一订单链路：Redis Lua 原子校验活动票额、票档库存和个人限购，生成 `queued` 订单后通过 Outbox + RabbitMQ 异步落库，消费者幂等推进至 `pending_payment`，避免入口请求同步竞争 MySQL 热行。
- 针对单票档库存行锁热点，将 MySQL 票档和抢票活动库存按用户稳定映射拆为 8 桶，并让父表退出消费热路径；在 1500 并发单活动测试中，`row_lock_waits` 中位数由 1359 降至 274，MQ 追平时间由约 22.9s 降至 11.4s，三轮均无超卖。
- 实现 `sync/batch` Outbox 写入模式：batch 使用有界内存缓冲和批量落库，配合失败回退、周期扫描补写和 publisher confirm；历史 500 单成功建单测试中，入口成功吞吐由约 327 QPS 提升至约 485 QPS，最终订单全部进入 `pending_payment`。
- 完成订单幂等、Redis/MySQL 兜底、RabbitMQ 重投递幂等、支付回调 HMAC/金额/事件号校验、支付超时关单、退款归还票额、电子票签发与并发核销，支付成功后才出票。
- 建立 Node/k6 压测与 Prometheus/Grafana/pprof/OpenTelemetry 观测链路，区分入口接受、MQ 消费、订单可支付和最终一致性指标；单机分桶档在恒定 VU 峰值测试中观测到约 1400 req/s 级入口吞吐，但尾延迟随并发继续升高。
- 搭建 Redis Sentinel、RabbitMQ quorum、三 Backend 和 MySQL 主从的本地故障演练拓扑，验证 Redis 主库切换、RabbitMQ 节点故障后的重连与消息恢复；明确 MySQL 仍是单主写入，未将该拓扑包装成生产级自动故障转移。

## 面试展开顺序

1. 先讲业务问题：抢票不能超卖，但入口不能把所有请求同步压到 MySQL。
2. 再讲链路：Redis Lua 预扣 → `queued` → Outbox → RabbitMQ → MySQL 确认 → `pending_payment`。
3. 再讲瓶颈：消费者每单更新父表导致全局热行，分桶只有在父表退出热路径后才有效。
4. 最后讲证据：`row_lock_waits`、MQ 追平、成功率、`still_queued`、订单最终状态分别对应不同问题。

## 组件与工具说明

| 组件/工具 | 在本项目中的作用 | 需要记住的边界 |
|---|---|---|
| Go | 后端语言；负责 HTTP、业务服务、消费者和后台任务 | goroutine 不是自动并行，数据库热行仍会串行化 |
| Gin | HTTP 路由和中间件 | Controller 只做参数/身份/响应包装，业务在 service |
| GORM | Go ORM 和事务封装 | `Transaction`、条件 UPDATE、`FOR UPDATE/SKIP LOCKED` 直接影响一致性和锁竞争 |
| MySQL/InnoDB | 订单、票额、支付、电子票最终事实来源 | Redis 预扣成功不等于 MySQL 已确认；行锁是消费侧瓶颈 |
| Redis | Lua 原子预扣、幂等快路径、限流和分布式锁 | 单 Redis 是前置闸门；Sentinel 是主从故障转移，不是多主 Redis |
| Redis Lua | 把“查库存 + 查限购 + 扣减 + 记录购买量”放在 Redis 内一次执行 | Lua 原子只保证 Redis 内部，仍要处理后续 DB/MQ 失败回滚 |
| RabbitMQ | Outbox publisher 与订单消费者之间的异步削峰、重试、DLX/延时关单 | publisher confirm 解决投递确认；消费仍需幂等 |
| Outbox | 将待投递消息持久化，降低 DB 与 MQ 双写不一致风险 | batch 档不是订单与 Outbox 同一个本地事务，靠补写收敛 |
| Snowflake | 生成分布式 int64 ID | 多实例必须使用不同 node ID |
| Vue 3 / Vite | 前端页面构建和开发服务器 | `npm.cmd` 适配 Windows PowerShell 策略限制 |
| Axios | 前端 HTTP 客户端、认证头、刷新和幂等键 | 订单创建是异步的，前端还要轮询/WebSocket 刷新状态 |
| WebSocket | 推送订单状态变化 | 只是体验优化，轮询仍是断线兜底 |
| Prometheus | 抓取请求、Outbox、MQ、库存补偿等指标 | 原始指标比截图更适合作为压测证据 |
| Grafana | 展示 Prometheus 指标和票务运行面板 | 看板不是压测结果本身 |
| OpenTelemetry / Jaeger | 跨 HTTP、Redis/GORM、Outbox、MQ 消费的 trace | tracing 默认关闭，采样率影响观测开销 |
| pprof | CPU/heap profile，定位热点函数 | 仅监听 loopback，生产不应长期开放 |
| k6 | 恒定 VU/阶梯模型，测入口 QPS 和 p99 | 不负责证明订单最终落库和不超卖 |
| Node.js 脚本 | 造数、真实业务请求、轮询状态、采样资源和校验结果 | 需要记录用户池、wave、限购和库存规模，否则数字不可比 |
| Docker Compose | 启动单机集成、容量栈和故障演练拓扑 | 不同 Compose 项目/卷/端口必须隔离 |
| HMAC | 校验支付回调和票码 | 只解决签名完整性，不等于真实支付渠道对账 |

## 不要过度表述

- 不说“支持多 Redis 集群”：当前是直连 Redis 或 Sentinel 主从发现，没有 Redis Cluster 多主分片。
- 不说“1500 QPS 的稳定支付吞吐”：1500 是特定本机压测口径，入口接受、MQ 追平和支付完成不是同一个指标。
- 不说“支付生产化”：当前是内置 sandbox，真实渠道查询、对账、重启恢复和退款重试仍未完成。
- 不说“MySQL 已自动高可用”：当前只验证主从复制，应用写入仍指向主库。
