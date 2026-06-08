# API 数据流

## 依据来源

- `backend/main.go`
- `backend/controller/user_controller.go`
- `backend/controller/address_controller.go`
- `backend/controller/product_controller.go`
- `backend/controller/order_controller.go`
- `backend/controller/seckill_controller.go`
- `backend/controller/admin_controller.go`
- `backend/service/*.go`
- `backend/pkg/response/response.go`

## 统一返回

成功:

```json
{"code":0,"msg":"ok","data":{},"request_id":"..."}
```

分页:

```json
{"code":0,"msg":"ok","data":{"list":[],"total":0,"page":1,"page_size":10},"request_id":"..."}
```

失败:

```json
{"code":400xx,"msg":"错误原因","request_id":"..."}
```

追问: 为什么响应里带 `request_id`？便于前端报错、日志和监控排查关联同一次请求。

## 用户端路由总览

公开接口:

- `POST /api/v1/login`
- `POST /api/v1/register`

鉴权接口:

- 订单: `POST /orders`, `GET /orders`, `GET /orders/:id`, `POST /orders/:id/cancel`, `POST /orders/:id/refund`
- 用户: `GET /user/info`, `PUT /user/info`, `PUT /user/password`
- 地址: `POST /addresses`, `GET /addresses`, `PUT /addresses/:id`, `DELETE /addresses/:id`, `PUT /addresses/:id/default`
- 商品: `GET /products`, `GET /products/:id`, `GET /categories`, `GET /categories/:id/products`
- 秒杀: `GET /seckill/activities`, `GET /seckill/activities/:id`, `POST /seckill/activities/:id/token`, `POST /seckill/activities/:id/execute`

## 用户与鉴权接口

### 注册: `POST /api/v1/register`

入参:

```json
{"username":"zhangsan","password":"xxx","dorm_id":1}
```

数据流:

1. Controller 使用 binding 和自定义 validator 校验用户名、密码、宿舍 ID。
2. `service.Register` 使用 bcrypt 生成密码哈希。
3. 创建用户，默认余额 `100.0`，绑定 `DormID`。

异常:

- 参数格式错误。
- 用户名唯一约束冲突。
- bcrypt 或 DB 写入失败。

追问:

- 为什么不存明文密码？密码泄露后风险不可控，bcrypt 可抗暴力破解。
- 默认余额是否真实支付？不是，是演示下单链路的简化。

### 登录: `POST /api/v1/login`

入参: `username`, `password`。

数据流:

1. Controller 参数校验。
2. `service.Login` 查询 `user`。
3. bcrypt 校验密码。
4. 更新 `last_login_at`。
5. 按配置 `jwt.expire_secs` 生成 JWT。

异常:

- 用户不存在。
- 密码错误。
- 参数格式错误。

追问:

- token 存在哪里？后端无状态签发，客户端保存并在 `Authorization` header 携带。
- token 里有什么？当前只放 `user_id` 和注册声明，不放敏感信息。

### 获取用户信息: `GET /api/v1/user/info`

数据流:

1. `AuthMiddleware` 解析 JWT，写入 `user_id`。
2. Controller 调 `common.GetUserID`。
3. `service.GetUserInfo` 查询用户部分字段，不返回密码。
4. 返回 `id`, `username`, `balance`, `dorm_id`, `phone`, `avatar_url`, `role`, `last_login_at`, `create_time`。

异常:

- 未登录或 token 无效。
- 用户不存在。

追问: 如何防止查看别人信息？接口不接收 user_id 参数，只从 JWT context 取当前用户。

### 更新用户信息: `PUT /api/v1/user/info`

入参:

```json
{"phone":"19800000000","avatar_url":"https://..."}
```

数据流:

1. 从 JWT 取当前用户 ID。
2. 绑定 `UpdateUserInfoReq`。
3. 如果传 phone，检查该手机号是否被其他用户使用。
4. 有更新字段才执行 `Updates`。

异常:

- 参数错误。
- 手机号被占用。
- DB 更新失败。

追问:

- 为什么不允许改 username？当前接口只开放 phone/avatar，降低唯一用户名变更复杂度。
- 手机号唯一由 DB 保证吗？当前主要是 service 查询检查；生产建议加唯一索引或唯一约束策略。

### 修改密码: `PUT /api/v1/user/password`

入参:

```json
{"old_password":"old","new_password":"new"}
```

数据流:

1. 从 JWT 取用户 ID。
2. 查询用户。
3. bcrypt 校验旧密码。
4. bcrypt 加密新密码。
5. 更新 `password` 字段。

异常:

- 用户不存在。
- 原密码错误。
- 新密码格式错误。

追问: 修改密码后旧 token 是否失效？当前不会立即失效，生产可加入 token version 或 Redis blacklist。

## 地址接口

### 创建地址: `POST /api/v1/addresses`

入参:

```json
{
  "receiver_name":"张三",
  "phone":"19800000000",
  "province":"湖北省",
  "city":"武汉市",
  "district":"武昌区",
  "detail":"武汉大学宿舍...",
  "is_default":false
}
```

数据流:

1. 从 JWT 取用户 ID。
2. Controller 绑定 `models.Address`。
3. `service.CreateAddress` 强制把 `addr.UserID` 设置为当前用户，忽略客户端伪造 user_id。
4. 查询该用户地址数量；如果是第一个地址，自动设为默认。
5. 创建地址。

异常:

- 未登录。
- 参数缺失，如 `receiver_name`, `phone`, `detail`。
- DB 创建失败。

追问: 如何防止用户给别人创建地址？后端覆盖 `UserID` 为 JWT 中用户 ID，不信任请求体。

### 地址列表: `GET /api/v1/addresses`

数据流:

1. 从 JWT 取用户 ID。
2. `service.ListAddresses` 按 `user_id` 查询。
3. 排序: `is_default DESC, create_time DESC`。

异常:

- 未登录。
- DB 查询失败。

追问: 为什么默认地址排前面？前端下单选择地址时更符合用户习惯。

### 更新地址: `PUT /api/v1/addresses/:id`

数据流:

1. 从 JWT 取用户 ID。
2. 解析地址 ID。
3. 按 `id` 和 `user_id` 查询地址，防越权。
4. 更新收货人、手机号、省市区、详细地址。

异常:

- 地址 ID 非法。
- 地址不存在或不属于当前用户。
- 参数错误。

追问: 更新接口会不会改默认状态？当前不会，只更新收货信息；默认地址通过单独接口处理。

### 删除地址: `DELETE /api/v1/addresses/:id`

数据流:

1. 从 JWT 取用户 ID。
2. 解析地址 ID。
3. 按 `id` 和 `user_id` 查询地址。
4. GORM 软删除。
5. 如果删的是默认地址，找该用户另一条地址设为默认。

异常:

- 地址不存在。
- DB 删除失败。

追问: 删除默认地址后没有其他地址怎么办？不会设置新默认，地址列表为空或无默认地址，由前端引导新增。

### 设置默认地址: `PUT /api/v1/addresses/:id/default`

数据流:

1. 从 JWT 取用户 ID。
2. 校验目标地址属于当前用户。
3. MySQL 事务内先把该用户所有默认地址置为 false。
4. 再把目标地址设为 true。

异常:

- 地址不存在或不属于当前用户。
- 事务失败。

追问: 数据库是否强制保证一个用户只有一个默认地址？当前没有唯一约束，service 事务保证常规路径；生产可加约束或锁增强并发安全。

## 商品与分类接口

### 商品列表: `GET /api/v1/products`

参数: `page`, `page_size`, `category_id`, `keyword`, `sort_by`。

数据流:

1. Controller 绑定 query。
2. `Normalize` 限制页码和页大小。
3. L1 本地缓存。
4. L2 Redis。
5. MySQL 查询并 `Preload("Category")`。
6. 回填本地缓存和 Redis。

追问: 为什么把 list 和 total 打包缓存？避免列表和总数分开缓存导致版本不一致。

### 商品详情: `GET /api/v1/products/:id`

数据流:

1. 解析商品 ID。
2. `service.GetProductDetail` 查询商品并预加载分类。
3. 返回商品详情。

异常:

- 商品 ID 非法。
- 商品不存在。

追问: 商品详情为什么没缓存？当前优先优化列表热点，详情热点可按 Cache Aside 增加 Redis 缓存。

### 分类列表: `GET /api/v1/categories`

数据流: 查询 `category`，按 `sort ASC, id ASC` 排序。

追问: 分类是否需要缓存？低频数据可后续加缓存，当前不是性能瓶颈。

### 分类商品: `GET /api/v1/categories/:id/products`

数据流:

1. 解析分类 ID。
2. 复用 `ProductListQuery`。
3. 强制设置 `query.CategoryID = &id`。
4. 走商品列表同一套 L1/L2/MySQL 逻辑。

追问: 为什么复用商品列表逻辑？避免重复实现分页、排序、缓存逻辑。

## 订单接口

### 普通下单: `POST /api/v1/orders`

入参:

```json
{"items":[{"product_id":1,"num":2}]}
```

数据流:

1. AuthMiddleware 得到 `user_id`。
2. Controller 绑定 body。
3. service 合并重复商品。
4. Redis Lua 校验所有商品库存并预扣。
5. 生成 Snowflake 订单 ID。
6. 发布 RabbitMQ 持久化消息。
7. HTTP 返回成功。
8. consumer 异步处理 MySQL 事务。

失败链路:

- 库存不足: Redis Lua 返回负数。
- MQ 投递失败: 回滚 Redis 预扣库存。
- DB 事务失败: consumer `Nack(false, true)` 重入队。
- 重复消费: consumer 根据 `order_id` 查重。

追问: 为什么 HTTP 返回时订单可能还没落库？因为下单链路异步化，前台代表“请求已进入处理队列”。生产应提供订单状态查询或轮询。

### 订单列表: `GET /api/v1/orders`

参数: `page`, `page_size`, `status`。

数据流:

1. 从 JWT 取用户 ID。
2. 页码和页大小归一化，`page_size` 最大 50。
3. 可选按订单状态过滤。
4. 统计 total。
5. `Preload("OrderItem.Product")`，按 `create_time DESC` 分页查询。

追问: 如何防越权？只查当前 `user_id` 的订单。

### 订单详情: `GET /api/v1/orders/:id`

数据流:

1. 从 JWT 取用户 ID。
2. 路径参数解析订单 ID。
3. 按 `id` 和 `user_id` 查询订单。
4. `Preload("OrderItem.Product")`。

追问: 为什么不能只按订单 ID 查？否则用户可能通过枚举订单 ID 查看别人订单。

### 取消订单: `POST /api/v1/orders/:id/cancel`

入参: `reason` 可选。

数据流:

1. 从 JWT 取用户 ID。
2. 查询当前用户订单。
3. `CanTransitionTo(OrderStatusCancelled)` 校验状态流转。
4. 事务内更新订单状态和取消原因。
5. 查询订单项，回补 MySQL 商品库存和 Redis 普通库存。
6. 回退用户余额。

异常:

- 订单不存在。
- 当前状态不允许取消。

追问: 为什么状态机重要？避免已退款、已取消等状态被重复取消或非法流转。

### 申请退款: `POST /api/v1/orders/:id/refund`

入参: `reason` 可选。

数据流:

1. 从 JWT 取用户 ID。
2. 查询订单。
3. 校验是否能流转到 `refunding`。
4. 事务内先标记 refunding。
5. 回补库存和 Redis 普通库存。
6. 回退余额。
7. 状态更新为 refunded，记录原因。

追问: 这个退款流程是否生产级？不是。当前是简化版，生产应有退款审核、支付渠道退款和异步回调。

## 秒杀接口

### 秒杀列表: `GET /api/v1/seckill/activities`

参数: `page`, `page_size`。

数据流: 分页查询秒杀活动，`Preload("Product")`。

追问: 秒杀列表为什么预加载商品？前端展示活动时通常需要商品名、图片、原价等信息。

### 秒杀详情: `GET /api/v1/seckill/activities/:id`

数据流: 按活动 ID 查询，预加载商品。

异常: 活动不存在返回 not found。

### 秒杀 token: `POST /api/v1/seckill/activities/:id/token`

数据流:

1. 鉴权取用户 ID。
2. 查询秒杀活动。
3. 校验活动时间和状态。
4. 读取 Redis 用户购买数量和库存。
5. 生成 HMAC token。
6. 写入 `seckill:token:{activityID}:{userID}`，60 秒过期。

追问: 为什么不直接执行秒杀？token 把“资格获取”和“执行秒杀”拆开，可以提高刷接口成本，并支持一次性校验。

### 秒杀执行: `POST /api/v1/seckill/activities/:id/execute`

入参:

```json
{"token":"...","quantity":1}
```

数据流:

1. 鉴权取用户 ID。
2. 查询活动得到限购和价格。
3. Redis Lua 校验 token、库存、限购，并执行扣减。
4. 生成订单 ID。
5. 发布 RabbitMQ。
6. MQ 投递失败则回滚秒杀库存和用户购买计数。
7. 更新 `remaining_stock`。

追问: Lua 为什么要删除 token？保证 token 一次性，降低重复提交。

## 管理端接口

管理端统一经过 `AuthMiddleware` 和 `AdminAuthMiddleware`。

### 仪表盘: `GET /api/v1/admin/dashboard`

数据流:

1. 校验管理员。
2. 统计用户数、订单数、总营收、今日订单、今日营收、在售商品数、活跃秒杀数。
3. 聚合热门商品 Top5。
4. 查询最近 7 天订单趋势。

追问: 聚合查询慢怎么办？生产可做离线统计、缓存、物化表或按天汇总表。

### 创建商品: `POST /api/v1/admin/products`

数据流:

1. 校验管理员。
2. 绑定 `models.Product`。
3. `AdminCreateProduct` 创建商品。
4. `refreshProductCache` 清商品列表缓存并刷新 `snack:stock:{id}`。

追问: 为什么创建后要刷新库存 key？下单入口依赖 Redis 库存。

### 更新商品: `PUT /api/v1/admin/products/:id`

数据流:

1. 解析商品 ID。
2. 绑定 updates map。
3. 按 ID 更新商品。
4. 清商品列表缓存并刷新库存 key。

异常:

- 商品不存在。
- 参数错误。

追问: updates map 有什么风险？字段白名单不严格，生产应限制可更新字段。

### 删除商品: `DELETE /api/v1/admin/products/:id`

数据流:

1. 按 ID 软删除商品。
2. 清商品列表缓存。
3. 删除 `snack:stock:{id}`。

追问: 删除商品会不会影响历史订单？软删除和订单项快照价能保留历史展示基础。

### 更新商品状态: `PUT /api/v1/admin/products/:id/status`

入参: `status`。

数据流:

1. 更新商品 `status`。
2. 清列表缓存。
3. 如果下架，`refreshProductCache` 会把库存 key 设为 0；如果上架，刷新为 DB 库存。

追问: 下架商品为什么库存 key 设 0？防止下单入口继续放行。

### 管理端订单列表: `GET /api/v1/admin/orders`

参数: `page`, `page_size`, `status`。

数据流:

1. 校验管理员。
2. 可选状态过滤。
3. `Preload("OrderItem.Product")` 和 `Preload("User")`。
4. 按 `create_time DESC` 分页返回。

追问: 管理端为什么能看所有订单？经过管理员权限校验；生产还应加审计日志。

### 管理端更新订单状态: `PUT /api/v1/admin/orders/:id/status`

入参: `status`。

数据流:

1. 查询订单。
2. `CanTransitionTo` 校验状态流转。
3. 如果目标是取消或退款完成，事务内更新状态、回补商品库存、回退用户余额。
4. 其他状态只更新订单状态。

追问: 管理端取消订单是否回补 Redis？当前 admin 取消/退款只回补 MySQL 库存和余额，没有像用户取消那样回补 Redis，这是一个需要修正的实现差异。

### 管理端用户列表: `GET /api/v1/admin/users`

数据流:

1. 校验管理员。
2. 分页查询用户。
3. `Select` 排除 password，只返回管理展示字段。

追问: 为什么要 `Select` 字段？避免把密码哈希等敏感字段返回给前端。

### 管理端更新用户角色: `PUT /api/v1/admin/users/:id/role`

入参: `role`。

数据流:

1. 校验管理员。
2. role 只允许 `user` 或 `admin`。
3. 按用户 ID 更新角色。

追问: 被降权用户旧 token 会不会立即失效？当前不会，生产应加 token version 或重新查角色。

### 创建秒杀活动: `POST /api/v1/admin/seckill`

数据流:

1. 校验管理员。
2. 绑定 `SeckillActivity`。
3. 写入 MySQL。

追问: 创建活动是否立即可抢？不一定，需要状态、时间和预热配合。

### 更新秒杀活动: `PUT /api/v1/admin/seckill/:id`

数据流: 按活动 ID 更新活动字段。

追问: 活动进行中能否修改库存？当前没有严格限制，生产应限制活动开始后的关键字段变更。

### 删除秒杀活动: `DELETE /api/v1/admin/seckill/:id`

数据流: GORM 删除活动。

追问: 删除活动是否清 Redis 秒杀 key？当前没有显式清理，生产应补活动收尾和缓存清理。

### 秒杀活动预热: `POST /api/v1/admin/seckill/:id/warmup`

数据流:

1. 查询活动。
2. 校验当前时间在活动开始和结束之间。
3. 写 Redis `seckill:stock:{activityID}`。
4. 更新活动 `status=active` 和 `remaining_stock=stock`。

追问: 为什么要管理端手动预热？避免每次请求懒加载库存打 DB，也让活动上线成为明确动作。

## API 总体风险口径

- 普通下单和秒杀下单都是异步落库，生产需要订单状态查询或推送。
- 当前管理端更新订单状态回补 Redis 的逻辑不如用户取消/退款完整，应修正。
- 地址没有订单快照，生产下单应保存收货信息快照。
- 部分管理接口使用 map 更新，生产应加字段白名单和审计日志。
- JWT 当前无刷新、黑名单、角色变更立即失效机制。
