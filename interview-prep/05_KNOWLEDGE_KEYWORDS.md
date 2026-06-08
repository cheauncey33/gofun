# 高频词词典

## 依据来源

- 本项目代码
- `go-interview/docs/Go`, `go-interview/docs/Redis`, `go-interview/docs/Mysql`, `go-interview/docs/Kafka`, `go-interview/docs/Network`, `go-interview/docs/Theory`
- 面经来源索引: `interview-prep/07_INTERVIEW_REPORTS_INDEX.md`
- 防偏离规则: `interview-prep/00_CONTEXT_LOCK.md`

## 使用规则

每个词条按四件事背: 是什么、为什么需要、项目有没有用、面试怎么说。项目没用的词，必须明确说“当前项目未实现”。

## Go 并发与运行时

| 词 | 是什么 | 项目有没有用 | 面试怎么说 |
| --- | --- | --- | --- |
| goroutine | Go 轻量级并发执行单元 | consumer workers、HTTP server、定时任务 | 用 goroutine 启动后台 consumer 和补偿任务，通过 context 控制退出 |
| channel | goroutine 间通信机制 | `quit` 信号、`ctx.Done()`、RabbitMQ delivery channel | 优雅停机里通过 channel 等待 OS 信号和 context cancel |
| context | 取消、超时、跨调用传递信号 | `mainCtx`, `shutdownCtx`, MQ publish | consumer 监听 `ctx.Done()`，HTTP shutdown 用 timeout context |
| select | 同时监听多个 channel | consumer、定时任务 | 用 `select` 同时处理退出信号和 ticker |
| WaitGroup | 等待多个 goroutine 完成 | consumer worker 退出 | 主 consumer 等待所有 worker 结束再返回 |
| mutex | 互斥锁 | MQ 发布 mutex | RabbitMQ publish channel 共享时用 mutex 避免并发写风险 |
| RWMutex | 读写锁 | IP limiter map | 读多写少场景用 RWMutex，创建 limiter 时双重检查 |
| GMP | Go runtime 调度模型 | goroutine 运行基础 | G 是 goroutine，M 是线程，P 是调度资源；项目 worker 由 runtime 调度 |
| GOMAXPROCS | 可并行执行 Go 代码的 P 数量 | 未显式配置 | 生产可结合 CPU 核数配置和观察，不要说项目手动调优过 |
| GC | 自动垃圾回收 | 所有 Go 程序基础 | 本地缓存和 limiter map 要注意生命周期和清理，降低长期内存增长 |
| 三色标记 | GC 标记算法思想 | 理论基础 | 解释白灰黑对象和写屏障，不要硬说项目调过 GC |
| STW | Stop The World，GC 暂停 | 理论基础 | Go GC 会尽量降低 STW，项目没有做 GC 调参 |
| 内存逃逸 | 变量从栈逃到堆 | 理论基础 | 可用 `go build -gcflags=-m` 分析，项目未专门优化 |
| defer | 函数退出时执行 | channel close、ticker stop、logger sync | 用于资源释放，注意循环中滥用 defer 的成本 |
| panic/recover | 异常中断与恢复 | 初始化核心依赖失败用 panic | DB/Redis/MQ 启动失败选择 fail fast |
| sync.Map | 并发 map | 未使用 | 项目用 map + RWMutex，因为需要自定义 limiter 创建逻辑 |
| sync.Pool | 临时对象复用池 | 未使用 | 高并发临时对象可优化 GC，项目暂不需要 |
| 原子操作 | CPU 级不可分割操作 | 未直接使用 | 可用于计数器；项目限流用库和锁实现 |
| goroutine 泄漏 | goroutine 永远阻塞不退出 | 需要关注 | 项目用 context 退出 consumer 和 ticker，降低泄漏风险 |
| race condition | 并发读写竞态 | IP limiter map 需防 | Go 原生 map 并发写会 panic，所以用锁保护 |
| gopark/goready | runtime 挂起/唤醒 G | 理论基础 | channel、mutex 阻塞底层会涉及调度，面试讲原理即可 |
| 栈扩容 | goroutine 栈按需增长 | 理论基础 | goroutine 初始栈小，适合大量并发 |

## Redis

| 词 | 是什么 | 项目有没有用 | 面试怎么说 |
| --- | --- | --- | --- |
| String | Redis 字符串 | 库存 key、token key | `snack:stock:{id}` 和 `seckill:token:{aid}:{uid}` 都是 String |
| Hash | Redis 哈希 | 秒杀用户购买计数 | `seckill:user_count:{activityID}` 记录 userID -> count |
| List | 链表/列表 | 未使用 | MQ 使用 RabbitMQ，不用 Redis List 做队列 |
| Set | 无序集合 | 未使用 | 可用于去重，项目当前不用 |
| ZSet | 有序集合 | 未使用 | 可用于排行榜，项目当前不用 |
| Lua | Redis 内原子执行脚本 | 普通下单和秒杀扣库存 | 把库存校验、限购、扣减放进同一个原子操作 |
| 单线程 | Redis 命令主执行线程模型 | 理论基础 | 单命令原子，多命令组合仍需 Lua |
| I/O 多路复用 | 一个线程处理多个连接事件 | 理论基础 | Redis 高性能原因之一 |
| 缓存穿透 | 查不存在数据反复打 DB | 未专门实现布隆过滤器 | 当前主要靠参数校验和短缓存，生产可加空值缓存/布隆过滤器 |
| 缓存击穿 | 热 key 失效瞬间打 DB | 商品列表可能遇到 | 当前 L1 30 秒 + L2 5 分钟降低风险，生产可加互斥重建 |
| 缓存雪崩 | 大量 key 同时过期 | 未做随机 TTL | 当前商品列表 TTL 固定，生产应加随机过期 |
| 热 key | 某个 key 被高频访问 | 商品列表、秒杀库存 | L1 本地缓存减少 Redis 网络压力 |
| 大 key | value 很大或元素很多 | 需要避免 | 商品列表缓存要控制 page_size，避免过大 value |
| 过期删除 | Redis 删除过期 key 的策略 | 商品列表、token TTL | token 过期自动失效，列表缓存定期回源 |
| 内存淘汰 | Redis 内存满后的淘汰 | 未配置 | 生产应结合缓存重要性设置 maxmemory-policy |
| RDB | 快照持久化 | 项目未配置 | 不能说项目做了 Redis 持久化，只能讲原理 |
| AOF | 追加日志持久化 | 项目未配置 | 生产库存 key 要考虑持久化和恢复策略 |
| pipeline | 批量发送命令 | 未使用 | 生产批量预热大量库存可考虑 pipeline |
| 事务 | MULTI/EXEC | 未使用 | 项目用 Lua，因为需要校验和扣减原子执行 |
| 分布式锁 | 跨进程互斥 | 未使用 Redlock | 本项目库存扣减用 Lua 原子操作，不靠分布式锁 |
| Redlock | 多 Redis 节点锁算法 | 未使用 | 被问到讲争议，不要说项目用了 |
| 布隆过滤器 | 快速判断不存在 | 未使用 | 可防缓存穿透，项目后续可加到商品 ID 校验 |
| 主从复制 | Redis 数据复制 | 未配置 | 不能说项目做了高可用 Redis |
| 哨兵/Cluster | Redis 高可用/分片 | 未实现 | 生产扩展项 |
| 一致性 | 缓存和 DB 状态收敛 | 基础回滚补偿 | 当前是最终一致基础版，不是强一致 |

## MySQL

| 词 | 是什么 | 项目有没有用 | 面试怎么说 |
| --- | --- | --- | --- |
| InnoDB | MySQL 默认事务引擎 | 默认使用 | 支持事务、行锁、MVCC |
| MyISAM | 老引擎 | 未使用 | 不支持事务，面试可对比 |
| 索引 | 加速查询的数据结构 | GORM tag 中有 index/uniqueIndex | user_id、status、category_id、activity_id 等字段建索引 |
| B+ 树 | InnoDB 常见索引结构 | 理论基础 | 适合范围查询和磁盘页顺序访问 |
| 聚簇索引 | 数据和主键索引放一起 | 主键索引 | InnoDB 主键是聚簇索引 |
| 二级索引 | 非主键索引 | 多个 index 字段 | 二级索引查到主键后可能回表 |
| 回表 | 二级索引再查主键索引 | 理论基础 | 查询字段不在索引中会回表 |
| 覆盖索引 | 索引包含所需字段 | 可优化查询 | 管理端用户列表可考虑 select + 覆盖索引优化 |
| 联合索引 | 多列索引 | 可扩展 | 如订单可建 `(user_id,status,create_time)` |
| 最左匹配 | 联合索引使用规则 | 理论基础 | 查询条件要从联合索引最左列开始匹配 |
| 索引失效 | 索引用不上 | 需避免 | 函数、隐式转换、前置 `%like` 等可能导致失效 |
| 事务 | 一组原子 DB 操作 | consumer 落库、取消/退款 | 订单、订单项、库存、余额在一个事务里 |
| ACID | 原子性、一致性、隔离性、持久性 | 理论基础 | 订单落库依赖事务保证内部一致 |
| 隔离级别 | 控制事务可见性 | 依赖 MySQL 默认配置 | 项目未显式配置，通常 InnoDB 默认 RR |
| MVCC | 多版本并发控制 | 理论基础 | 解释快照读和当前读，不要说项目手写 MVCC |
| 当前读 | 读最新并加锁/受锁影响 | update 扣库存 | 条件 update 属于当前读/写 |
| 快照读 | 读事务视图 | 普通 select | 订单查询通常是快照读 |
| 行锁 | 更新行时加锁 | 条件扣库存触发 | `WHERE id=? AND stock>=? UPDATE stock=stock-?` |
| 间隙锁 | 锁索引间隙 | 未直接设计 | 被问到讲 RR 防幻读，不硬套项目 |
| 临键锁 | 记录锁 + 间隙锁 | 未直接设计 | 理论题回答即可 |
| redo log | 崩溃恢复日志 | 理论基础 | 保证事务持久性 |
| undo log | 回滚和 MVCC | 理论基础 | 事务失败回滚依赖数据库机制 |
| binlog | 主从复制/恢复 | 未配置主从 | 不能说项目做了主从复制 |
| 慢 SQL | 执行慢的 SQL | 需关注 | 压测时要看订单事务和商品查询慢 SQL |
| 分库分表 | 数据水平拆分 | 未实现 | 生产订单量大后可按 user_id/order_id 分片 |

## RabbitMQ / MQ

| 词 | 是什么 | 项目有没有用 | 面试怎么说 |
| --- | --- | --- | --- |
| producer | 消息生产者 | API 下单入口 | Redis 预扣后发布订单消息 |
| consumer | 消息消费者 | order workers | 后台异步执行 MySQL 事务 |
| queue | 队列 | `order_queue` | 订单消息进入队列等待消费 |
| exchange | 交换机 | 使用默认 exchange | 当前直接 publish 到队列名 |
| routing key | 路由键 | 使用队列名 | 默认 exchange 下 routing key 等于队列名 |
| durable queue | 持久化队列 | 已使用 | `QueueDeclare` durable=true |
| persistent message | 持久化消息 | 已使用 | `DeliveryMode: Persistent` |
| publisher confirm | 生产者确认 | 未实现 | 生产可靠性必须补，当前不能说已实现 |
| ack | 消费确认 | 成功后手动 ack | 防止业务没处理完就确认 |
| nack | 拒绝/失败确认 | 失败 `Nack(false,true)` | 业务失败重新入队 |
| auto ack | 自动确认 | 关闭 | 项目关闭 auto ack，避免消息提前确认 |
| prefetch | 未 ack 消息窗口 | 配置 `prefetch_count` | 控制每个 worker 同时持有的消息数 |
| requeue | 重新入队 | nack 使用 | 可恢复错误重试，永久错误会反复重试 |
| 死信队列 | 失败消息隔离 | 未实现 | 生产扩展项，不能说已实现 |
| 延迟队列 | 延时投递 | 未实现 | 可用于订单超时取消，项目当前没有 |
| 重复消费 | 同一消息可能多次处理 | 用 order_id 幂等 | MQ 至少一次语义下必须业务幂等 |
| 消息丢失 | 消息未成功处理 | 基础防护 | durable + persistent + manual ack，但缺 confirm |
| 消息积压 | 生产速度大于消费速度 | 简历中压测观察 backlog | 调 worker/prefetch，但也要看 DB 压力 |
| unacked | 已投递未确认消息 | 压测需观察 | 过高说明 consumer 持有但处理慢 |
| ready | 队列待投递消息 | 压测观察 | ready 高说明消费能力不足或下游慢 |
| 顺序消息 | 保持处理顺序 | 未保证 | 订单链路不依赖全局顺序 |
| 幂等 | 重复处理结果一致 | order_id 查重 | 比依赖 MQ 不重复更可靠 |
| RabbitMQ vs Kafka | 业务队列 vs 日志流 | 用 RabbitMQ | 项目需要订单任务队列，不需要 Kafka 日志吞吐 |
| 本地消息表 | DB 记录消息状态 | 未实现 | 生产补可靠最终一致可加 |

## 高并发与分布式

| 词 | 是什么 | 项目有没有用 | 面试怎么说 |
| --- | --- | --- | --- |
| 高并发 | 同时大量请求 | 秒杀/下单场景 | 项目用 Redis/MQ/限流降低 DB 压力 |
| QPS | 每秒请求数 | 压测关注 | 不能只看 QPS，还要看 RT、错误率 |
| TPS | 每秒事务数 | 订单消费关注 | consumer 落库吞吐更接近 TPS |
| RT | 响应时间 | 压测关注 | 异步化降低入口 RT |
| TP95/TP99 | 分位耗时 | 生产压测指标 | 当前脚本需进一步补分位统计 |
| 限流 | 控制入口流量 | 全局/IP 令牌桶 | 防止恶意或突发流量打穿服务 |
| 令牌桶 | 按速率生成令牌，允许突发 | 已用 | 适合秒杀瞬时突发 |
| 漏桶 | 固定速率流出 | 未用 | 更强调平滑，不适合保留突发能力 |
| 削峰 | 平滑突发请求 | RabbitMQ | 把瞬时写库变成后台排队消费 |
| 异步化 | 请求和重操作解耦 | 下单链路 | HTTP 快速返回，DB 事务后台处理 |
| 降级 | 牺牲部分能力保核心 | 未完整实现 | Redis/MQ 故障时可关闭秒杀或只读 |
| 熔断 | 故障比例高时短路 | 未实现 | 生产可对下游依赖加熔断 |
| 幂等 | 多次执行结果一致 | order_id 查重 | 重复消息不重复创建订单 |
| 防重提交 | 避免用户重复请求 | 秒杀 token | 普通订单还需 idem key 扩展 |
| 最终一致 | 延迟达到一致 | Redis/DB 回滚补偿 | 当前是基础版本，不是强一致 |
| 强一致 | 操作后立即一致 | 未实现 | 高并发下成本高，项目采用最终一致思路 |
| 补偿 | 异常后修正状态 | 库存补偿任务 | 定时发现并修正部分库存异常 |
| 少卖 | 库存没卖完但系统显示无货 | 可能出现 | 补偿不能盲目调大，要结合消息状态 |
| 超卖 | 卖出超过库存 | 重点防 | Redis Lua 前置 + MySQL 条件扣减兜底 |
| 热点隔离 | 热点请求隔离处理 | 部分实现 | 秒杀活动独立库存 key，生产可独立服务/队列 |
| 连接池 | DB/Redis 连接复用 | MySQL max open, Redis pool | Redis 拦截无效流量，保护 DB 连接池 |
| 背压 | 下游慢时限制上游 | MQ/prefetch 相关 | worker/prefetch 控制消费压力 |
| 压测 | 模拟负载验证系统 | Node.js 脚本 | 观察 MQ backlog 并调 worker/prefetch |
| 观测性 | 指标/日志/追踪 | 基础 Prometheus + 日志 | 还需补齐业务埋点和告警 |

## Web 工程与网络

| 词 | 是什么 | 项目有没有用 | 面试怎么说 |
| --- | --- | --- | --- |
| HTTP | 应用层协议 | Gin API | 前端通过 REST API 请求后端 |
| REST | 资源风格 API | 基本符合 | `/orders`, `/products`, `/seckill` 等 |
| Gin middleware | 请求链路中间处理 | 鉴权、限流、监控、CORS | 横切逻辑统一处理 |
| JWT | 无状态 token | 登录鉴权 | token 携带 user_id，服务端验签 |
| CORS | 跨域资源共享 | Gin cors | 允许前端 dev origin |
| Request ID | 请求唯一标识 | 已实现 | 响应中返回 request_id，便于日志关联 |
| 统一响应 | 固定返回格式 | `Response` | 包含 code/msg/data/request_id |
| 错误码 | 业务错误分类 | `error_code.go` | 便于前后端处理 |
| 参数校验 | 校验请求合法性 | validator/binding | 防止非法参数进入业务链路 |
| bcrypt | 密码哈希 | 用户密码 | 不存明文密码 |
| 日志 | 记录运行信息 | zap | 生产排障依据 |
| 日志切割 | 控制日志文件大小 | lumberjack | 避免日志无限增长 |
| Prometheus | 指标采集体系 | `/metrics` | HTTP 请求、延迟、连接数等 |
| Histogram | 直方图指标 | HTTP 延迟 | 用于观察请求耗时分布 |
| Counter | 只增计数器 | 请求数等 | 适合统计总请求数 |
| Gauge | 可增可减指标 | 活跃连接 | 适合当前状态值 |
| 优雅停机 | 停机前收尾 | context + Shutdown | 先通知后台任务，再关闭 HTTP |
| TCP 三次握手 | 建立连接过程 | 理论基础 | HTTP 底层依赖 TCP |
| TIME_WAIT | 主动关闭方等待状态 | 理论基础 | 高并发短连接可能遇到 |
| HTTP keep-alive | 复用 TCP 连接 | 理论基础 | 减少连接建立成本 |
| HTTPS | HTTP over TLS | 未配置 | 生产部署需要 TLS 终止 |
| Nginx | 反向代理 | deploy 配置 | 可做静态资源、反代、TLS、限流 |
| Docker | 容器化 | Dockerfile/compose | 项目有部署文件，但不要夸大为 K8s 生产经验 |
| Linux 信号 | 进程控制信号 | 优雅停机 | 监听 SIGINT/SIGTERM |
| pprof | Go 性能分析 | 未接入 | 生产排查 CPU/内存可加 |
