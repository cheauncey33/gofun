# Gofun 电子票与核销

## 目标

本阶段把“支付成功”转换为可验证的入场资格：

```text
票务订单
  -> 订单明细 quantity = N
  -> N 张 AdmissionTicket
  -> 每张票独立核销
  -> 每次核销尝试写 TicketVerificationRecord
```

订单负责交易和退款，电子票负责入场资格，核销记录负责审计证据。三者不能用同一个状态代替。

## 数据库基线

迁移文件位于 `backend/migrations/`：

- `001_ticketing_baseline.sql`：冻结主办方、活动、票档和票务订单的 V1 最小结构。
- `002_admission_ticket.sql`：增加电子票与核销记录。
- `schema_migration`：记录已经执行的版本。

旧数据库首次启动时，迁移器会确认 V1 的 11 张表全部存在后登记基线；如果只存在部分表，
启动会失败，不会把残缺结构误标为完成。新数据库按文件名顺序执行全部迁移。

MySQL 的 DDL 不具备完整事务回滚能力。迁移使用 `CREATE TABLE IF NOT EXISTS` 降低重复执行风险，
但 V1 只创建了部分表时会停止启动并要求人工检查，不会自动把残缺结构登记为完成。生产部署前仍应
先备份数据库并在副本验证。

## 电子票

`AdmissionTicket` 一行代表一个可入场单位。订单明细购买 3 张，就创建 3 行。

关键约束：

- `ticket_no` 唯一：对外展示票号。
- `(order_item_id, sequence_no)` 唯一：支付重试或历史补签不能重复发票。
- 状态只允许当前阶段使用 `valid`、`used`、`revoked`。
- 二维码凭证不落库，由电子票 ID 和 `ticket_qr.secret` 生成 HMAC 签名。

二维码格式为 `FC1.<payload>.<signature>`。前端二维码库只负责绘制字符串，真实性由后端验证。
更换 `ticket_qr.secret` 会导致旧二维码失效，因此密钥必须独立保存并纳入备份。

## 支付与退款

支付采用“外部结果先回调，平台本地事务出票”的边界：

```text
创建 payment_transaction（平台只保存支付单，不保存用户余额）
-> 支付沙箱/真实渠道异步回调
-> 验签 + provider_event_id 幂等
-> 锁定待支付订单
-> 按购买数量创建电子票
-> 订单改为 paid
-> 提交
```

回调失败不会出票，订单保持待支付并记录 `payment_status=failed`，允许重新创建支付单；支付超时由订单超时消费者关单并释放票额。

退款会先检查订单中是否有 `used` 电子票：

- 已核销：拒绝退款，防止“先入场再退款”。
- 未核销：调用支付 provider 退款，成功后将全部 `valid` 电子票改为 `revoked`。
- 待支付订单：没有电子票，只归还票额。

## 核销

接口：

- `POST /api/v1/organizers/:organizer_id/verifications`
- `GET /api/v1/organizers/:organizer_id/verifications`

核销流程：

```text
验证主办方成员权限
-> 验证票码 HMAC
-> SELECT ... FOR UPDATE 锁定电子票
-> 检查主办方与票状态
-> valid 条件更新为 used
-> 写入核销记录
-> 提交
```

`success_ticket_id` 只在成功记录中写入，并有唯一索引。行锁是主要并发控制，唯一索引是数据库
兜底，因此同一张票的两个并发请求只能有一个成功。

失败票码、重复扫码、作废票和跨主办方扫码同样会留记录；数据库只保存票码 SHA-256 指纹，
不保存完整票码。

## 密钥轮换

`ticket_qr.secret` 用于签发新票。轮换时把旧密钥写入 `ticket_qr.previous_secrets`，
验签会依次尝试当前密钥与旧密钥；二维码格式仍为 `FC1.<payload>.<signature>`。

## 当前不包含

- 电子票转赠和实名持票人
- 座位图和座位号
- 部分退款
- 离线核销与设备同步
- 真实第三方支付渠道（已有 `PaymentGateway` 适配层，当前为 `SandboxPaymentGateway`）

## 验证

```powershell
cd backend
go test ./... -count=1

cd ../frontend
npm.cmd run build
```

集成环境中至少验证：

1. 购买 2 张并支付后生成 2 张电子票。
2. 同一票码并发核销，只有一次 `success`。
3. 核销后的订单不能退款。
4. 未核销订单退款后所有电子票为 `revoked`。
5. 作废票核销返回 `revoked`，并留下失败记录。
