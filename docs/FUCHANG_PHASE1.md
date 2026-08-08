# Gofun 第一阶段改造说明

## 1. 真正解决的问题

旧项目的校园零食电商场景无法自然解释高并发开售、异步削峰和票额一致性。
第一阶段不是把 Go 改成 Java，也不是替换变量名，而是把核心业务对象和运行链路改成活动票务。

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

启动时使用“MySQL 剩余票额减去 queued 占用”重建 Redis，并重新投递 queued 订单。
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

- 用户表暂时同时存在旧 `balance` 和新 `balance_cents`。运行中的票务链路只使用后者。
- 旧零食代码暂时保留但不注册运行路由，便于逐步核对与回滚；验证稳定后再删除。
- 当前 queued 恢复属于轻量恢复策略，不是完整事务 Outbox。若要追求更强的消息投递证明，
  下一阶段应增加 outbox 表、投递状态和独立转发器。
- 支付超时：进入 `pending_payment` 后投递 RabbitMQ 延时消息（消息级 TTL + DLX），到期条件取消；DB 扫描器作兜底。
- 前端管理台尚未实现；第一阶段先验证用户购票主链路和管理 API。

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

回滚代码可切回标签 `pre-fuchang-phase1-20260721`。数据库回滚不要直接删除新表；
应先备份并确认没有需要保留的票务订单。
