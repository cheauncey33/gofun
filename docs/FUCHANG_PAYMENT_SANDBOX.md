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

已支付订单取消时，服务会先在数据库事务内检查已核销票并将订单标记为 `refunding`，再调用 provider `Refund`，成功后以统一锁顺序完成本地退款、票额回补和电子票作废。两者仍不是一个分布式事务；如果本地提交失败，订单会保留 `refunding` 状态，后续重试可以继续收敛。

真实接入支付宝/微信时，不是只替换一个适配器就完成：还要补渠道查询、回调/退款幂等重试、对账、可恢复状态和密钥轮换，并将 provider 通过依赖注入配置。

## 当前限制

- 沙箱回调计时器仍保存在进程内，但启动时会从 `payment_transaction` 恢复待支付、已支付和已退款状态，并重新调度仍有效的待回调交易。
- 重试未完成支付会复用同一支付单，并重新注册沙箱状态和回调调度，不会创建第二笔支付。
- 支付回调、取消和超时路径统一按“支付记录 -> 订单”的数据库锁顺序；核验先锁订单，再锁电子票，退款中的订单拒绝核验。
