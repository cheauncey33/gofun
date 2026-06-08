# 项目白盒手册

## 依据来源

- `backend/main.go`
- `backend/service/product_service.go`
- `backend/service/order_service.go`
- `backend/service/order_consumer.go`
- `backend/service/seckill_service.go`
- `backend/service/compensation_service.go`
- `backend/common/*.go`
- `backend/metrics/metrics.go`

## 一句话介绍

这是一个 Go + Gin 实现的校园电商/零食秒杀系统。核心不是普通 CRUD，而是围绕“高并发下单”做了 Redis 预扣库存、RabbitMQ 异步削峰、消费端幂等落库、失败回滚、库存补偿、限流和监控。

## 整体架构

```text
Vue3 前端
  |
  | HTTP + JWT
  v
Gin API
  |-- RequestID / Prometheus / 全局限流 / IP限流 / CORS
  |-- AuthMiddleware: JWT -> user_id
  |
  |-- MySQL: 用户、商品、订单、订单项、秒杀活动、秒杀订单
  |-- Redis: 商品库存、秒杀库存、秒杀用户限购、秒杀 token、商品列表缓存
  |-- LocalCache: 商品列表 L1 缓存
  |-- RabbitMQ: order_queue
  |
  v
Order Consumer Workers -> MySQL 事务落库
```

## 启动流程

1. `config.Load` 加载配置。
2. `logger.Init` 初始化 zap/lumberjack 日志。
3. `gin.SetMode` 设置运行模式。
4. 初始化基础设施: JWT secret、MySQL、Redis、RabbitMQ、Snowflake、本地缓存。
5. `validator.Init` 初始化参数校验。
6. `InitProductStockToRedis` 把商品库存预热到 `snack:stock:{productID}`。
7. 注册 Gin middleware: request id、Prometheus、全局限流、IP 限流、CORS。
8. 注册 `/api/v1` 用户路由和 `/api/v1/admin` 管理路由。
9. 注册 `/metrics`。
10. 启动 order consumer workers。
11. 启动 HTTP server。
12. 启动 IP limiter 清理任务和库存补偿任务。
13. 监听 `SIGINT/SIGTERM`，执行优雅停机。

面试回答重点: 初始化失败使用 `panic`/`Fatal` 是 Fail Fast。数据库、Redis、MQ 是核心依赖，带病启动会造成更隐蔽的数据问题。

## 鉴权链路

注册和登录不需要 JWT。登录成功后 `service.Login` 用 bcrypt 校验密码，并通过 `GenerateToken` 生成 JWT。鉴权接口进入 `AuthMiddleware`，后端从 `Authorization` header 取 token，调用 `ParseToken`，成功后 `c.Set("user_id", claims.UserID)`。

回答口径:

- 项目没有从请求体信任 `user_id`，而是从 JWT claims 获取。
- Controller 通过 `common.GetUserID(c)` 取当前用户，避免越权传参。
- 当前实现没有 `Bearer ` 前缀解析逻辑，面试被问到可以说这是一个可改进点。

## 商品查询链路

`GetProductList` 使用三级读取:

1. L1 本地缓存 `common.LocalCache.Get(cacheKey)`。
2. L2 Redis `GET product:list:...`，命中后反填本地缓存。
3. L3 MySQL，按 `status = on_sale`、分类、关键词、排序、分页查询。

写回策略:

- MySQL 查询后写本地缓存和 Redis。
- Redis TTL 5 分钟。
- 本地缓存默认 30 秒，自动清理 1 分钟。
- 管理端创建、更新、删除、上下架商品时调用 `clearProductCache`，并刷新 `snack:stock:{id}`。

面试回答重点: L1 减少应用到 Redis 的网络 IO，L2 减少 MySQL 压力。当前项目没有做分布式缓存一致性协议，只是用短 TTL + 管理端主动清理降低不一致窗口。

## 普通下单链路

正常路径:

1. `POST /api/v1/orders` 进入 `CreateOrderHandler`。
2. 从 JWT context 取 `user_id`。
3. 绑定 `CreateOrderInput`。
4. `normalizeCreateOrderInput` 合并同商品数量并校验数量。
5. Redis Lua `deductStockLua` 对多个 `snack:stock:{productID}` 先检查再统一扣减。
6. Snowflake 生成 `orderID`。
7. 序列化 `OrderMessage`。
8. `PublishPersistent` 投递到 RabbitMQ `order_queue`。
9. consumer 收到消息后调用 `ProcessOrderTask`。
10. consumer 先按 `order_id` 查重，防重复消费。
11. MySQL 事务内查询商品、条件扣 DB 库存、扣用户余额、创建订单、创建订单项、增加销量。
12. consumer 成功后 `Ack`。

失败路径:

- Redis Lua 返回负数: 库存不足，不进入 MQ。
- MQ 投递失败: 逐个 `IncrBy` 回滚 Redis 普通库存。
- consumer 处理失败: `Nack(false, true)` 重新入队。
- 重复消息: `order_id` 已存在则直接返回成功并 ack。

当前不足:

- 没有生产者 confirm，不能严格证明消息已经落到 broker 磁盘。
- 没有死信队列，持续失败可能造成反复重入队。
- 金额用 `float64` 承载，虽然 GORM 标记 `decimal(10,2)`，Go 内存计算仍有精度隐患。

## 秒杀链路

token 获取:

1. `POST /seckill/activities/:id/token`。
2. 校验用户 JWT。
3. 查询活动，校验开始时间、结束时间、活动状态。
4. 从 Redis 读取 `seckill:user_count:{activityID}` 和 `seckill:stock:{activityID}` 做快速判断。
5. 生成 `activityID:userID:timestamp` 的 HMAC-SHA256 token。
6. 写入 `seckill:token:{activityID}:{userID}`，TTL 60 秒。

执行秒杀:

1. `POST /seckill/activities/:id/execute`。
2. 入参 token 和 quantity，quantity 默认 1。
3. 查询活动得到 `limit_per_user` 和价格。
4. Redis Lua 原子执行:
   - 校验 token 存在且匹配。
   - 删除 token，确保一次性。
   - 校验库存。
   - 校验用户限购。
   - `DECRBY` 秒杀库存。
   - `HINCRBY` 用户购买数量。
5. 成功后生成订单 ID，投递 MQ。
6. MQ 投递失败时回滚秒杀库存和用户计数。
7. 更新活动 `remaining_stock`。

面试回答重点: 秒杀不是只靠 MySQL 行锁，而是把大部分失败请求挡在 Redis 内存层；Lua 保证 token、库存、限购三个判断和扣减在 Redis 内原子完成。

## MQ 消费链路

`StartOrderConsumer` 根据配置启动多个 worker。每个 worker:

- 独立 `NewMQChannel`。
- 设置 `Qos(prefetchCount, 0, false)`。
- `Consume` 时关闭 auto ack。
- JSON 解析失败时 `Ack` 丢弃坏消息。
- 业务处理成功 `Ack`。
- 业务处理失败 `Nack(false, true)` 重新入队。
- channel 异常时 3 秒后重连。

面试回答重点:

- `prefetch` 控制每个 worker 未 ack 消息数量，避免单 worker 被大量消息压住。
- 手动 ack 保证“业务处理完成后再确认”。
- 幂等靠订单 ID 查重，不靠 MQ 恰好只投递一次。

## 库存补偿

当前 `RunStockCompensation` 每 5 分钟扫描普通商品:

- Redis 库存 key 缺失时，按 DB 库存重建。
- Redis 库存大于 DB 库存时，认为有超卖风险，按 DB 修正。

回答口径:

- 这是一个基础补偿任务，不是完整分布式事务。
- 对普通商品库存更明确；秒杀场景还需要结合活动库存、秒杀订单和用户购买计数做更精细补偿。
- 生产扩展可用本地消息表、死信队列、补偿任务和告警闭环。

## 限流链路

项目有两层令牌桶:

- 全局 limiter: 所有请求共享。
- IP limiter: `map[string]*rate.Limiter`，每个 IP 一个 limiter。

`IPRateLimiter.GetLimiter` 使用 `RWMutex` 和双重检查:

1. 读锁查 map。
2. 不存在时加写锁。
3. 写锁内再次检查，避免并发重复创建。
4. 定时 `CleanupIPLimiters`，当 map 超过 100000 时重置。

回答口径: 当前是单进程限流。多实例部署时，需要 Redis + Lua 或网关层限流。

## 监控与日志

Prometheus 指标:

- `http_requests_total`
- `http_request_duration_seconds`
- `active_http_connections`
- `orders_created_total`
- `orders_cancelled_total`
- `seckill_requests_total`
- `stock_compensation_total`
- `mq_messages_published_total`
- `mq_messages_consumed_total`

当前注意点: 指标定义较完整，但部分业务指标未在所有业务分支中打点。面试时可以说“已接入基础 HTTP 监控，业务指标定义了，后续应补齐关键路径埋点”。

## 连续追问题库

### 启动与初始化

1. 服务启动时先初始化 MySQL 还是 Redis？为什么？
2. `AutoMigrate` 做了什么？生产环境能不能直接用？
3. 初始化 DB 失败为什么直接 `panic`？
4. Redis 初始化失败时系统能否降级启动？
5. RabbitMQ 初始化失败时普通商品查询还能不能用？为什么代码没有这么做？
6. 商品库存为什么启动时预热到 Redis？
7. 如果预热过程中服务崩溃，可能出现什么问题？
8. 多实例同时启动时，库存预热会不会覆盖运行态 Redis 库存？
9. Snowflake node id 从哪里来？多机部署怎么避免重复？
10. 优雅停机为什么先 cancel consumer，再 shutdown HTTP？

回答要点: 当前项目按核心依赖 Fail Fast。`AutoMigrate` 适合开发和演示，生产建议使用版本化 migration。多实例预热是风险点，生产应把预热和活动上线做成明确运维动作，避免覆盖运行态库存。

### 鉴权与用户身份

1. JWT 里放了哪些信息？
2. 为什么只放 `user_id`，不放密码或手机号？
3. token 在哪里校验？
4. 为什么 Controller 不信任请求体里的 `user_id`？
5. `Authorization` header 格式是什么？支持 `Bearer` 吗？
6. token 过期后返回什么？
7. 管理员权限在哪里校验？
8. 如果用户角色被修改，旧 token 是否立即失效？
9. JWT secret 泄露会怎样？
10. 如何做 token 刷新和黑名单？

回答要点: 当前 JWT 是无状态鉴权，`AuthMiddleware` 验签后把 `user_id` 写入 Context。当前没有 Bearer 前缀兼容、刷新 token、黑名单和角色变更立即失效机制，生产可用短 access token + refresh token + Redis blacklist 扩展。

### 商品与缓存

1. 商品列表缓存 key 包含哪些维度？
2. 为什么 `page/page_size/category/keyword/sort` 都要进 key？
3. L1 本地缓存和 Redis 谁先查？
4. 本地缓存 TTL 为什么比 Redis 短？
5. 商品更新后如何清缓存？
6. `SCAN product:list:*` 有什么风险？
7. 多实例下本地缓存怎么失效？
8. 商品详情为什么没有走 Redis 缓存？
9. 缓存击穿怎么处理？
10. 热点商品详情如何优化？

回答要点: 项目使用 Cache Aside。列表缓存 key 必须包含查询维度，否则会串数据。当前主动清理只对当前进程本地缓存生效，多实例需要 Redis pub/sub、消息广播或统一网关缓存。`SCAN` 比 `KEYS` 安全，但大量 key 下仍要控制批量和频率。

### 普通下单

1. 下单入参为什么要 normalize？
2. 同一个商品传两次会怎样？
3. Lua 为什么先检查所有库存再扣减？
4. 如果第一个商品扣了、第二个不足会怎样？
5. Redis 扣减成功后为什么生成订单 ID？
6. 为什么 HTTP 不等 DB 事务完成？
7. MQ 发布失败怎么回滚？
8. consumer 落库失败怎么处理？
9. 用户余额不足时会发生什么？
10. 普通下单重复点击是否完全幂等？

回答要点: normalize 合并重复商品，避免同一订单内重复 key 带来的扣减混乱。Lua 先检查再扣减保证多商品预扣的原子性。当前普通下单没有请求幂等 key，重复点击可能生成多个订单，这是可改进点。

### 秒杀

1. 秒杀为什么分 token 和 execute 两步？
2. token 用什么算法生成？
3. token 为什么存 Redis？
4. token 为什么 60 秒过期？
5. Lua 里为什么要先校验 token 再删除？
6. 用户限购计数存在什么结构里？
7. 秒杀库存和普通商品库存是什么关系？
8. `remaining_stock` 为什么还要更新 DB？
9. 秒杀 MQ 发布失败回滚哪些 key？
10. 秒杀活动结束后 Redis key 怎么处理？

回答要点: token 是资格校验和防重复提交手段，不是安全的唯一防线。Lua 删除 token 保证一次性。当前活动结束后的 Redis key 清理、秒杀补偿和 `(user_id, activity_id)` DB 唯一约束还可增强。

### MQ 与 Consumer

1. 为什么每个 worker 单独开 channel？
2. `Qos(prefetchCount, 0, false)` 的三个参数含义是什么？
3. 为什么关闭 auto ack？
4. JSON 解析失败为什么 ack 而不是 nack？
5. 业务失败为什么 nack requeue？
6. requeue 会有什么风险？
7. worker 异常退出如何恢复？
8. MQ 连接断开怎么处理？
9. producer 端是否线程安全？
10. 为什么 `PublishPersistent` 加了 mutex？

回答要点: RabbitMQ channel 通常不建议并发共享写，所以发布端用 mutex，消费端每个 worker 单独 channel。解析失败属于坏消息，重试无意义，所以 ack 丢弃；业务失败可能是 DB 抖动，所以 requeue，但生产应加重试次数和死信队列。

### MySQL 事务与幂等

1. consumer 为什么先查订单是否存在？
2. 只查不加锁是否足够？
3. 订单表主键能否兜底？
4. 库存扣减 SQL 为什么要带 `stock >= num`？
5. 余额扣减为什么要带 `balance >= totalAmount`？
6. 为什么订单项里保存 `SnapshotPrice`？
7. 订单创建成功、订单项失败会怎样？
8. 销量更新失败会怎样？
9. 秒杀订单表写入失败会怎样？
10. 如何区分可重试和不可重试 DB 错误？

回答要点: 幂等检查要靠数据库唯一约束兜底。条件更新避免并发下库存或余额变负。事务确保订单、订单项、库存、余额、销量一起成功或一起失败。

### 库存补偿

1. 补偿任务多久跑一次？
2. 它扫描哪些表？
3. Redis key 缺失怎么处理？
4. Redis 库存大于 DB 库存怎么处理？
5. Redis 库存小于 DB 库存为什么当前没有修？
6. 秒杀库存补偿是否完整？
7. 订单失败后 Redis 回滚失败怎么办？
8. 补偿任务会不会误伤正在进行的活动？
9. 如何记录补偿日志和告警？
10. 生产级补偿表怎么设计？

回答要点: 当前补偿是普通库存基础补偿，不是完整最终一致闭环。Redis 小于 DB 可能代表预扣未落库导致少卖，需要结合订单消息状态判断，不能盲目按 DB 改大。生产需要补偿记录表、重试状态、告警和人工修复入口。

### 限流

1. 为什么既有全局限流又有 IP 限流？
2. 令牌桶和漏桶区别是什么？
3. 为什么选择令牌桶？
4. `rate.NewLimiter(r,b)` 的 r 和 b 是什么？
5. IP limiter map 为什么要锁？
6. 为什么用 `RWMutex`？
7. 双重检查解决什么问题？
8. map 超过 100000 为什么直接重置？
9. 单机限流多实例部署有什么问题？
10. 如何改造成 Redis 分布式限流？

回答要点: 全局限流保护整体服务，IP 限流限制单来源滥用。令牌桶允许一定突发，更适合秒杀入口。当前是单机版，多实例需要 Redis Lua、网关限流或服务网格限流。

### 监控与压测

1. 当前暴露了哪些 Prometheus 指标？
2. HTTP 延迟用什么指标类型？
3. 为什么要记录 active connections？
4. 业务指标定义了但是否都打点了？
5. 压测时看 QPS 够不够？
6. RabbitMQ ready 和 unacked 分别代表什么？
7. worker 调大后看哪些副作用？
8. 如何定位瓶颈在 Redis、MQ 还是 MySQL？
9. Grafana 面板应该放哪些图？
10. 如何设计告警阈值？

回答要点: 当前 HTTP 基础指标可用，部分业务指标需要补齐埋点。压测不能只看 QPS，还要看 RT、TP99、错误率、DB 连接池、Redis 延迟、MQ ready/unacked 和 consumer 处理耗时。
