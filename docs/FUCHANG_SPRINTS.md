# Gofun Sprint A / B / C 执行计划

目标：把票务从「可演示主链路」推进到「可运营、可并发、可现场」。
原则：小步可逆；每项都有可验证行为；不引入新框架。

## 总览

| Sprint | 主题 | 交付物 | 验收 |
|--------|------|--------|------|
| A | 产品可运营 | 下架/停售、批量退款、排队体验、清零食遗留 | 主办方可停售并退未核销票；用户看见排队；主路径无零食依赖 |
| B | 并发可信 | 票额对账、Outbox、抢票限流/压测、指标 | 漂移可纠；发布可恢复；压测脚本可跑；Prometheus 有票务序列 |
| C | 现场可用 | 核销加固、支付适配层、票码版本 | 核销可按场次约束；支付可切换实现；密钥可多版本验签 |

## Sprint A — 产品可运营

### A1 下架 / 停售
- `POST .../events/:id/unpublish`：`published → draft`（仅当无 paid 订单时可回草稿；否则只允许 cancel）
- `POST .../events/:id/cancel`：活动取消；场次/票档停售；清 Redis 票额键
- `POST .../sessions/:id/ticket-tiers/:id/disable`：票档停售并删 Redis 键

### A2 批量退款
- `POST .../events/:id/refunds`：对活动下全部 `paid` 且无 `used` 票的订单批量退款
- 单笔仍复用 `CancelOrder`；跳过已核销订单并返回汇总

### A3 订单排队体验
- `OrderDetail` / `Cashier`：queued 自动轮询 + 明确文案/进度
- 可选：列表展示 queued 徽章

### A4 清零食遗留
- 删除未接线的 snack controller/service/view（保留迁移回滚标签说明）
- 支付展示统一为“支付沙箱/待渠道回调”，平台不展示或维护外部用户余额
- container 去掉 ProductRepo 若无引用

## Sprint B — 并发可信

### B1 票额对账
- `TicketCompensationService`：每 5 分钟对齐 `fuchang:ticket:stock:*` 与 MySQL（Redis > 可用量则下调）
- 启动仍保留 `WarmTicketQuota`

### B2 Outbox
- 表 `ticket_order_outbox`：订单创建后写 outbox，后台转发 MQ
- CreateOrder / rush create 不再直接依赖 Publish 成功才返回（或：同事务写 outbox，异步投递）
- 启动时扫未投递记录

### B3 抢票限流 + 压测 + 指标
- 对 `POST /rush-sales/:id/execute` 与 `POST /orders` 加更严 IP/用户限流配置
- `tests/load/ticket_smoke.mjs` + `ticket_rush_spike.mjs`
- metrics：ticket orders created/paid/failed、rush execute、compensation、outbox publish

## Sprint C — 现场可用

### C1 核销端强化
- 核销可选 `session_id` 约束
- 面板：今日成功数、失败分类、历史更清晰

### C2 支付适配层
- `PaymentGateway`：创建支付单、异步回调、退款
- `SandboxPaymentGateway` + `payment_transaction` / `payment_callback` 已落地
- 订单只保存支付结果，不读取或扣减用户余额；真实渠道可替换适配器

### C3 票码密钥轮换
- `ticket_qr.secrets` 支持多版本：`FC1.<kid>.<payload>.<sig>` 或并行验旧签新
- 签发用当前密钥；校验遍历有效密钥

## 执行顺序

```text
A1 → A2 → A3 → A4 → B1 → B2 → B3 → C2 → C3 → C1
```

## 落地状态（2026-07-21）

本轮已按上表最小切片落地，详见 `docs/FUCHANG_COLLAB.md`。

验证建议：

```powershell
cd backend
go test ./service/ ./models/ ./config/ ./container/ -count=1

# 依赖就绪后
go run . -config ./config/config.yaml
# 另开终端
node tests/load/ticket_smoke.mjs
# RUSH_CAMPAIGN_ID=<id> node tests/load/ticket_rush_spike.mjs
```

## 非目标（本三 Sprint 不做）

- 选座、转赠、第三方真实支付渠道对接、离线核销设备同步、结算分账
