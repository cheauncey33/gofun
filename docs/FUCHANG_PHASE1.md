# Gofun 第一阶段改造说明

## 1. 真正解决的问题

第一阶段把核心业务对象和运行链路收敛为活动票务，重点验证高并发开售、异步削峰和票额一致性。

## 2. 范围与假设

当前采用“多主办方、无选座票务”：

1. 平台管理员创建主办方并指定负责人。
2. 主办方维护场馆、活动、场次和票档。
3. 用户浏览已发布活动，选择票档购票。
4. 热门场次可配置限时开售。
5. 支付使用内置支付沙箱：支付单由沙箱创建，订单只等待异步回调，不读取或扣减 Gofun 账户余额。

本阶段仍不实现选座、真实微信/支付宝渠道、结算分账和离线核销设备同步。

## 3. 核心模型

- `Organizer / OrganizerMember`：主办方及成员角色。
- `Venue`：城市、行政区、详细地址、经纬度和时区。
- `Event`：活动内容、分类、发布状态、实名开关和单笔限购。
- `EventSession`：演出时间、销售时间、场馆、状态和版本号。
- `TicketTier`：价格（分）、总票额、剩余票额、销量、限购和版本号。
- `TicketOrder / TicketOrderItem`：订单状态、支付状态、来源、幂等键和活动快照。
- `PaymentTransaction / PaymentCallback`：平台侧支付单、沙箱/渠道回调和幂等审计，不保存用户外部余额。
- `RushSaleCampaign`：限时价格、独立票额、个人限购和开售时间。

订单明细保存活动、场次、场馆、地址和票档名称快照。否则主办方修改活动后，
历史订单会显示新内容，无法作为购票时事实记录。

## 4. 核心链路

### 普通购票

```text
POST /api/v1/orders
-> 同步创建 queued 订单凭据
-> Redis Lua 预扣 fuchang:ticket:stock:<tierID>
-> 发布 fuchang.order.queue
-> 消费者锁定订单和票档
-> 扣减 MySQL 票额
-> queued -> pending_payment
```

### 限时开售

```text
到点一次 POST /rush-sales/:id/execute
-> Lua 同时检查活动票额、票档票额、个人限购
-> 创建 rush_sale 来源的 queued 订单
-> 复用普通订单消费者
```

### 支付与超时

```text
pending_payment -> 创建 payment_transaction -> provider callback(success) -> paid -> 签发电子票
provider callback(failed) -> payment_status=failed，可重试
pending_payment 到期 -> cancelled -> MySQL 与 Redis 归还票额
paid 取消 -> provider refund -> payment_status=refunded，并归还票额
```

### 崩溃恢复

Redis 预扣会同步写 pending 凭证；统一库存恢复 Worker 扫描遗留凭证，并通过 MySQL `order_id` 唯一栅栏决定确认或回滚，避免与未提交订单事务竞态。
启动时先恢复遗留凭证，再使用“MySQL 剩余票额减去 queued 占用”重建 Redis；运行期低频执行同口径总量对账。
消费者只处理 queued 状态，所以重复投递不会重复扣减 MySQL。

## 5. API

公开：

- `GET /api/v1/events`
- `GET /api/v1/events/:id`
- `GET /api/v1/rush-sales`

登录用户：

- `POST /api/v1/orders`
- `GET /api/v1/orders`
- `GET /api/v1/orders/:id`
- `POST /api/v1/orders/:id/pay`
- `POST /api/v1/orders/:id/cancel`
- `POST /api/v1/rush-sales/:id/execute`

主办方成员：

- `POST /api/v1/organizers/:organizer_id/venues`
- `POST /api/v1/organizers/:organizer_id/events`
- `POST /api/v1/organizers/:organizer_id/events/:event_id/sessions`
- `POST /api/v1/organizers/:organizer_id/sessions/:session_id/ticket-tiers`
- `POST /api/v1/organizers/:organizer_id/events/:event_id/publish`
- `POST /api/v1/organizers/:organizer_id/rush-sales`

平台管理员：

- `POST /api/v1/admin/organizers`
- `GET /api/v1/admin/organizers`

## 6. 已知代价与后续

- 用户表仍保留历史余额字段，但票务支付不读取、不扣减用户余额；金额走支付单的 `int64` 分字段。
- 当前已使用事务 Outbox、投递状态、publisher confirm 和恢复任务；支付回调本身仍是进程内沙箱调度，
  重启恢复、渠道查询、对账和退款重试需要后续补强。
- 支付超时：进入 `pending_payment` 后投递 RabbitMQ 延时消息（消息级 TTL + DLX），到期条件取消；DB 扫描器作兜底。
- 前端已包含结算、订单和主办方运营台；成员邀请、复杂财务结算和真实渠道接入不在本阶段。

## 7. 验证与回滚

```powershell
cd backend
go test ./... -count=1

cd ../frontend
npm.cmd run build
```

运行时至少验证：

```text
创建主办方 -> 创建场馆/活动/场次/票档 -> 发布
-> 普通购票 -> queued -> pending_payment -> 支付
-> 超时取消并核对 MySQL/Redis 票额
-> 限时开售个人限购与幂等键
-> 重启后 queued 重投且不重复扣减
```

代码回滚应使用项目实际存在的 Git 提交或分支。数据库回滚不要直接删除新表；
应先备份并确认没有需要保留的票务订单，优先通过前向迁移修复。
