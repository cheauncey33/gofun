# 数据库表设计与追问

## 依据来源

- `backend/models/model.go`
- `backend/models/category.go`
- `backend/models/address.go`
- `backend/models/seckill_activity.go`
- `backend/models/seckill_order.go`
- `backend/common/databaseInit.go`
- `backend/service/order_service.go`
- `backend/service/address_service.go`
- `backend/service/admin_service.go`

## 公共字段

所有主要业务表嵌入 `Base`:

| 字段 | 含义 | 面试回答 |
| --- | --- | --- |
| `id` | 主键，项目中订单显式使用 Snowflake ID | 异步下单前就需要订单 ID，用于消息、幂等和追踪 |
| `update_time` | 自动更新时间 | 便于审计和排查 |
| `create_time` | 自动创建时间 | 支持排序、趋势统计 |
| `delete_time` | GORM 软删除 | 商品、地址等删除后可保留历史数据，不直接物理删除 |

通用追问:

1. 为什么要有 `delete_time`？为了保留历史数据，尤其订单关联历史商品时不能因为删除商品导致历史订单不可读。
2. 软删除有什么风险？唯一索引可能被历史软删数据占用，查询时也要知道 GORM 默认会过滤软删记录。
3. 为什么订单 ID 用 Snowflake？MQ 异步落库前就要生成订单 ID，用于消息体、幂等和排查。
4. `create_time` 和 `update_time` 谁维护？GORM 通过 `autoCreateTime`、`autoUpdateTime` 自动维护。
5. 生产中会直接用 `AutoMigrate` 吗？不建议。生产应使用版本化 migration，避免自动迁移造成不可控结构变更。

## 表清单

### `user`

字段: `username`, `password`, `balance`, `phone`, `avatar_url`, `dorm_id`, `role`, `last_login_at`。

关系:

- `dorm_id` 关联 `dormitory.id`。
- `order.user_id`、`address.user_id`、`seckill_order.user_id` 指向用户。

细追问:

1. 为什么 `password` 不返回前端？`json:"-"` 会在序列化时忽略密码字段，避免泄露密码哈希。
2. 为什么用 bcrypt？bcrypt 带盐且计算成本可调，比普通哈希更抗暴力破解。
3. `balance float64` 有什么问题？Go 层浮点计算有精度隐患，生产建议改 `int64` 分或 decimal 库。
4. `phone` 为什么是指针？允许手机号为空，区分“没填”和空字符串。
5. `role` 如何控制管理权限？登录后 JWT 只保存 user_id，管理端请求通过 `AdminAuthMiddleware` 再查/校验角色。
6. 用户名唯一在哪里体现？`Username` 有 `gorm:"unique"`，注册重复会失败。
7. 默认余额为什么是 100？这是项目演示支付流程的简化，不代表真实充值系统。
8. `last_login_at` 有什么用？用于安全审计、用户活跃度和管理端展示。

### `dormitory`

字段: `building_name`, `room_number`。

作用: 校园场景的宿舍信息，注册用户需要 `dorm_id`。

细追问:

1. 为什么要有宿舍表？项目面向校园零食/电商场景，宿舍是配送和用户画像字段。
2. 宿舍表是不是高并发核心表？不是，它是低频基础资料表。
3. 注册时为什么传 `dorm_id`？用户需要绑定宿舍，后续可用于配送和地址默认值扩展。
4. 是否有唯一约束防止同楼栋同房间重复？当前模型没有，需要生产上加 `(building_name, room_number)` 唯一索引。
5. 如果宿舍被删除，用户怎么办？当前没有外键级联策略，生产应限制删除或做迁移。
6. 为什么不直接把宿舍字符串存在 user？拆表方便规范化、统计和后续维护。

### `address`

字段: `user_id`, `receiver_name`, `phone`, `province`, `city`, `district`, `detail`, `is_default`。

关系: 一个用户多个地址。

细追问:

1. 如何防止用户改别人的地址？更新、删除、设置默认都按 `id` 和 `user_id` 一起查询。
2. 首个地址如何成为默认地址？`CreateAddress` 统计用户地址数量，数量为 0 时设置 `IsDefault=true`。
3. 默认地址如何保证唯一？`SetDefaultAddress` 用事务先把该用户旧默认清空，再把目标地址设为默认。数据库层当前没有唯一约束。
4. 删除默认地址后怎么办？如果删除的是默认地址，service 会找该用户另一个地址设为默认。
5. 删除地址是硬删还是软删？嵌入 `Base`，GORM delete 默认软删。
6. 当前默认地址方案有什么并发风险？并发设置默认地址时，只有事务但无唯一约束，极端情况下仍建议加用户维度唯一约束或锁。
7. 为什么下单表里 `address_id` 可空？当前下单流程简化，没有强制绑定地址，生产订单应强制快照收货信息。
8. 为什么订单不应该只关联地址 ID？历史地址可能被用户修改，生产更适合在订单中保存收货信息快照。

### `category`

字段: `name`, `parent_id`, `sort`。

关系:

- `product.category_id` 指向分类。
- `parent_id` 支持层级分类。

细追问:

1. 分类名为什么唯一？避免分类名重复导致管理和查询歧义。当前是全局唯一。
2. 全局唯一有什么限制？不同父分类下不能有同名子分类，生产可改成 `(parent_id, name)` 唯一。
3. `parent_id` 为什么可空？顶级分类没有父分类。
4. `sort` 有什么用？前端展示和管理端排序。
5. 删除分类时商品怎么办？当前没有专门约束，生产应禁止删除有商品的分类或迁移商品。
6. 为什么商品表中 `category_id` 是指针？允许商品暂时不归类，降低录入门槛。
7. 分类查询有没有缓存？当前只查 DB，分类低频可接受，后续可加缓存。

### `product`

字段: `name`, `description`, `price`, `stock`, `image_url`, `category_id`, `status`, `sales_count`。

关系:

- `category_id` 关联 `category`。
- `order_item.product_id` 关联商品。
- `seckill_activity.product_id` 关联商品。

细追问:

1. `stock` 在 MySQL 和 Redis 都有，谁为准？MySQL 是最终事实，Redis 是高并发入口预扣状态。
2. 如何避免 MySQL 层超卖？consumer 事务中用 `WHERE id=? AND stock>=?` 条件更新。
3. `status` 有什么作用？控制上下架，商品列表只查在售商品。
4. 商品上下架后如何处理缓存？管理端会清本地缓存、删除 Redis 商品列表缓存，并刷新库存 key。
5. 为什么 `price` 有风险？Go 层 `float64` 计算金额有精度问题，生产应改成分或 decimal。
6. 为什么有 `sales_count`？便于热门商品排序和管理端统计，但要在订单事务中同步更新。
7. 商品删除会影响历史订单吗？软删除保留历史数据，订单项还有商品快照价格。
8. 商品详情为什么没有缓存？当前重点缓存列表，详情热点可后续加 Cache Aside。
9. `image_url` 为什么只存 URL？避免数据库存二进制大对象，静态资源交给对象存储或前端资源服务。

### `order`

字段: `user_id`, `address_id`, `total_price`, `status`, `cancel_reason`。

状态:

- `1 pending`
- `2 paid`
- `3 delivering`
- `4 delivered`
- `5 cancelled`
- `6 refunding`
- `7 refunded`

状态流转由 `CanTransitionTo` 控制。

细追问:

1. 为什么订单 ID 用 Snowflake？异步落库前就要有稳定 ID，且可用于 MQ 幂等。
2. 为什么 consumer 处理前查重？MQ 是至少一次语义，重复投递时不能重复创建订单。
3. 当前下单直接 `paid` 合理吗？这是简化支付流程，生产应拆支付单、支付状态和回调。
4. 取消订单如何退库存和余额？事务内更新订单状态、商品库存和用户余额，同时回补 Redis 普通库存。
5. 退款流程当前是否真实？当前 `RequestRefund` 会直接退款完成，是简化版，生产要有审核和支付渠道退款。
6. `address_id` 可空说明什么？当前下单未强制地址，是项目简化点。
7. `total_price` 为什么也有金额精度风险？同 `balance`，生产改 `int64` 分。
8. 状态机有什么价值？防止从任意状态跳到任意状态，比如已取消订单不能再配送。
9. 管理员改状态是否安全？`AdminUpdateOrderStatus` 也调用 `CanTransitionTo`，但仍需操作审计。
10. 订单查询如何防越权？用户端详情查询条件包含 `id` 和 `user_id`。

### `order_item`

字段: `order_id`, `product_id`, `quantity`, `snapshot_price`。

关系:

- 多个订单项属于一个订单。
- 订单项保存商品和价格快照。

细追问:

1. 为什么不把商品列表 JSON 存在订单表？不利于查询、统计、索引和销量聚合。
2. 为什么需要 `snapshot_price`？商品价格变化不应影响历史订单金额。
3. `quantity` 如何校验？入口 `binding` 和 `normalizeCreateOrderInput` 都要求大于 0。
4. 订单项创建失败怎么办？在同一 MySQL 事务内，失败会回滚订单、库存、余额等操作。
5. 为什么 `Product` 也放在结构体里？GORM 预加载时用于返回订单项对应商品信息。
6. `order_id` 是否应建索引？当前 GORM 未显式标注，生产查询订单详情高频时建议建索引。
7. `product_id` 是否应建索引？统计商品销量、查询商品订单时有价值，生产建议建索引。
8. 秒杀订单项价格怎么处理？如果 `OrderMessage.SeckillPrice > 0`，订单项快照价使用秒杀价。

### `seckill_activity`

字段: `name`, `product_id`, `seckill_price`, `stock`, `remaining_stock`, `start_time`, `end_time`, `limit_per_user`, `status`。

关系: 关联一个商品。

细追问:

1. 为什么秒杀库存独立于商品库存？活动库存是营销库存，便于控制秒杀份额。
2. `stock` 和 `remaining_stock` 区别是什么？`stock` 是活动初始库存，`remaining_stock` 是当前剩余展示/管理字段。
3. Redis 秒杀库存和 `remaining_stock` 如何一致？抢购时以 Redis 为入口状态，成功后更新 DB；生产应加活动维度补偿。
4. 为什么要有 `start_time` 和 `end_time`？控制活动有效窗口，token 获取阶段会校验。
5. 为什么还需要 `status`？时间只是条件之一，管理端还需要显式控制 pending/active/ended。
6. `limit_per_user` 如何落地？Redis Hash `seckill:user_count:{activityID}` 记录用户购买数量。
7. 活动预热做了什么？校验时间后写 `seckill:stock:{activityID}`，更新状态和剩余库存。
8. 活动结束后 Redis key 怎么办？当前没有专门清理，生产应加过期或活动收尾任务。
9. 秒杀价也用 `float64` 有什么风险？同金额字段，生产建议改分或 decimal。

### `seckill_order`

字段: `user_id`, `activity_id`, `order_id`, `amount`。

关系:

- 记录用户参与某秒杀活动生成的订单。
- `order_id` 有唯一索引。

细追问:

1. 为什么需要秒杀订单表？把秒杀活动维度从普通订单拆出来，便于活动统计、核对和风控。
2. 如何防止重复秒杀？Redis user_count 前置拦截，DB 层当前有 `order_id` 唯一；生产建议加 `(user_id, activity_id)` 唯一索引。
3. `amount` 存什么？该秒杀订单金额，用于活动营收统计和核对。
4. 为什么不只在普通订单表加 `activity_id`？单独表让普通订单保持通用，也便于秒杀专项统计。
5. 秒杀订单创建失败怎么办？在 consumer MySQL 事务内，失败会回滚普通订单、订单项、库存和余额。
6. `order_id` 唯一解决什么？解决同一个订单消息重复消费时重复插入秒杀订单。
7. 如果用户同一活动生成两个不同订单 ID 怎么办？当前主要靠 Redis user_count，生产应加 `(user_id, activity_id)` 唯一兜底。
8. 秒杀订单和活动库存如何核对？生产可按 `activity_id` 聚合秒杀订单数量，与活动库存和 Redis 剩余库存对账。

## 当前设计风险与回答

- 金额字段: 项目里用 `float64`，GORM 映射 `decimal(10,2)`。回答时要主动说生产建议改为 `int64` 分或 decimal。
- 软删除: 有利于历史数据，但查询要注意默认过滤和唯一索引影响。
- 唯一索引: `username` 唯一，`seckill_order.order_id` 唯一。生产防重复秒杀建议加 `(user_id, activity_id)`。
- 状态机: 已有 `CanTransitionTo`，比任意改状态更安全，但支付流程仍是简化版。
- 地址快照: 当前订单只关联 `address_id`，生产应在订单中保存收货信息快照。
- 消息幂等: 当前靠订单 ID 查重和主键兜底，生产应更明确捕获 duplicate key 并视为幂等成功。
