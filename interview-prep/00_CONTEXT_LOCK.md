# WHU Snack 面试材料防偏离总控

## 依据来源

- 简历 PDF: `张雨豪-19826563920-武汉大学-暑期求职.pdf`
- 入口与路由: `backend/main.go`
- 数据模型: `backend/models/model.go`, `backend/models/category.go`, `backend/models/address.go`, `backend/models/seckill_activity.go`, `backend/models/seckill_order.go`
- 核心服务: `backend/service/order_service.go`, `backend/service/seckill_service.go`, `backend/service/order_consumer.go`, `backend/service/product_service.go`, `backend/service/compensation_service.go`
- 基础设施: `backend/common/databaseInit.go`, `backend/common/redis.go`, `backend/common/rabbitMQ.go`, `backend/common/ratelimit.go`, `backend/common/jwt.go`, `backend/common/localcache.go`
- 监控与响应: `backend/metrics/metrics.go`, `backend/pkg/response/response.go`

## 项目真实边界

项目名称在简历中写作 `Campus-Mall 校园电商秒杀系统`，仓库名为 `WHU_Snack_GO`。面试回答时可以说这是一个基于校园零食/电商场景的 Go 后端项目，重点训练高并发下单、秒杀、缓存、MQ 削峰和最终一致性。

真实技术栈:

- 后端: Go, Gin, GORM
- 数据库: MySQL
- 缓存: Redis, go-cache 本地缓存
- 消息队列: RabbitMQ, `github.com/rabbitmq/amqp091-go`
- 鉴权: JWT, Gin middleware
- ID: Snowflake
- 监控: Prometheus client, `/metrics`
- 日志: zap, lumberjack
- 前端: Vue3, Vite
- 部署相关文件: Dockerfile, docker-compose, nginx 配置

## 已确认数据表

`AutoMigrate` 当前注册 9 张表:

1. `dormitory`
2. `category`
3. `order`
4. `order_item`
5. `user`
6. `product`
7. `address`
8. `seckill_activity`
9. `seckill_order`

所有表通过 `Base` 继承公共字段: `id`, `update_time`, `create_time`, `delete_time`。`delete_time` 是 GORM 软删除字段。

## 已确认核心链路

- 登录注册: bcrypt 存密码, 登录成功生成 JWT, 中间件解析 `Authorization` 并把 `user_id` 写入 Gin Context。
- 商品查询: 商品列表走 L1 本地缓存 -> L2 Redis -> MySQL；结果缓存为 `ProductListCache`，Redis TTL 5 分钟，本地缓存默认 30 秒。
- 普通下单: Redis Lua 一次性校验并预扣多个商品库存 -> Snowflake 生成订单 ID -> RabbitMQ 持久化投递 -> consumer 异步事务落库。
- 秒杀 token: 校验活动时间和状态 -> HMAC-SHA256 生成一次性 token -> Redis 保存 60 秒。
- 秒杀执行: Redis Lua 原子校验 token、库存、用户限购并预扣 -> RabbitMQ 异步投递订单消息 -> consumer 落库。
- MQ 消费: 多 worker, 每个 worker 单独 channel, `Qos(prefetchCount)`, 手动 ack；成功 `Ack`, 失败 `Nack(false, true)` 重新入队。
- 幂等: consumer 在落库前按 `order_id` 查询，已存在则直接返回成功。
- 库存补偿: 定时扫描商品库存，发现 Redis key 缺失或 Redis 库存大于 DB 库存时重建/修正普通商品库存 key。
- 限流: 全局令牌桶 + IP 令牌桶，使用 `sync.RWMutex` 保护 IP 到 limiter 的 map。
- 优雅停机: `mainCtx` cancel 通知 consumer 和定时任务退出，HTTP server 使用 `Shutdown` 等待连接收尾。
- 监控: Gin middleware 统计请求数、延迟、活跃连接数；注册 `/metrics`。

## 禁止幻觉清单

以下内容不能说成项目已经实现:

- 没有实现 Redis Cluster、Sentinel、Redlock。
- 没有实现 RabbitMQ 死信队列、延迟队列、重试次数上限、生产者 confirm。
- 没有实现分布式事务、TCC、SAGA、本地消息表。
- 没有实现 Kubernetes、服务网格、真实线上灰度发布。
- 没有真实生产千万 QPS，只能说写过压测脚本并观察到 MQ backlog 与 consumer 调优效果。
- 没有实现 WebSocket 或订单状态异步推送。
- 没有实现完善的库存补偿闭环来处理所有秒杀异常场景；当前普通库存补偿更明确。
- 没有完整的多机分布式限流；当前限流是进程内令牌桶。

## 回答口径

- 已实现能力: 直接讲代码链路、关键数据结构、异常分支和不足。
- 未实现但能扩展: 必须先说“当前项目没有做”，再讲生产扩展方案。
- 简历过强表述: 改成“实践过”“实现了基础版本”“通过压测脚本观察并调优”，避免说成真实生产经验。
- 面试被追问到不知道: 先承认边界，再把回答拉回项目中已经做过的部分。

## 后续写作规则

- 每篇文档开头必须写依据来源。
- 每个结论尽量能指向文件或函数。
- 八股词条必须落到“项目有没有用、怎么用、没用如何回答”。
- 外部面经必须保留 URL，不能只写“网上说”。
- 每完成一篇文档，回查本文件，删除或标注任何超出真实项目的描述。
