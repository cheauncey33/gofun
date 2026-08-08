# Gofun 支付沙箱

当前环境使用 `SandboxPaymentGateway` 模拟微信/支付宝这类外部支付机构。它不读取、扣减或保存 `user.balance_cents`；平台只保存自己的支付单、订单状态和渠道回调审计。

## 成功链路

```text
POST /api/v1/orders/:id/pay {"scenario":"success"}
-> 创建 payment_transaction(pending)
-> 沙箱延迟发送签名回调
-> POST /api/v1/payments/sandbox/callback
-> provider_event_id 幂等 + 金额校验
-> pending_payment -> paid
-> 签发电子票
```

出票只发生在成功回调事务内。支付接口返回的是 `pending` 支付单，而不是“已支付”。

## 可测试场景

- `success`：约 500ms 后回调成功并出票。
- `failed`：回调失败，订单仍待支付，`payment_status=failed`，可以重试。
- `timeout`：不发送回调，由订单超时消费者关单并释放票额。
- `manual`：不自动回调，便于手动构造渠道通知。

## 退款与真实渠道替换

已支付订单取消时，调用 provider `Refund`，成功后才在本地事务作废电子票并标记退款。真实接入支付宝/微信时，只需替换 `PaymentGateway`：保留支付单、回调验签、回调幂等、金额/订单校验、查询重试和对账机制。
