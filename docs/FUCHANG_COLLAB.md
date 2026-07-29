# 赴场协作文档

本文档供 Cursor / Codex / 人工在同一仓库并行改票务时对齐进度。
事实以当前代码为准；阶段文档（`FUCHANG_*.md`）描述目标，本文件描述**正在发生的协作状态**。

## 角色约定

| 角色 | 职责 |
|------|------|
| Codex | 主改造：票务模型、购票链路、主办方闭环、核销 |
| Cursor（本会话） | Code review + 缺陷修复 + 维护本协作文档 |
| 用户 | 需求拍板、验收、决定是否提交/开 PR |

## 当前状态（2026-07-21）

### 已完成基线（Codex）

- 票务目录：主办方 / 场馆 / 活动 / 场次 / 票档
- 普通购票：queued → Redis 预扣 → MQ → pending_payment → pay → 电子票
- 限时开售：token + Lua + 复用订单消费者
- 核销：HMAC 票码 + 行锁 + 审计记录
- 主办方工作台前端与收银台/结账页

### 本轮 Cursor 已修复（来自 review）

| 优先级 | 问题 | 状态 | 位置 |
|--------|------|------|------|
| P1 | 抢票幂等命中后未回滚多余 Redis 预扣 | 已修 | `rush_sale_service.go`：`rollback` 标志 |
| P1 | 抢票绕过联系人/须知/实名校验 | 已修 | Execute 接收 `PurchaseInfoInput`；前端弹层 |
| P1 | campaign DB 瞬时错误被标成不可重试 | 已修 | `ProcessOrderTask` 仅 `ErrRecordNotFound` 不可重试 |
| P2 | 已发布活动仍可加票档且不预热 Redis | 已修 | `CreateTicketTier` 拒绝 published |
| P2 | 支付超时从入队起算，排队吃掉窗口 | 已修 | 转入 `pending_payment` 时重写 `expires_at` |
| P2b | 超时仅靠 DB 扫描，关单滞后 | 已修 | 延时队列主路径 + 扫描兜底 |
| P2 | 归还票额强制 `on_sale`，复活已下架票档 | 已修 | `restoreTierQuota` 仅 sold_out→on_sale |
| P2 | disabled 主办方仍可写操作/核销 | 已修 | catalog + verification 门禁查 organizer.status |
| P2 | 异常 queued 明细恢复时 Redis 不归还 | 已修 | `RecoverQueuedOrders` 按明细回滚 |

### Sprint A/B/C 执行进度（见 `docs/FUCHANG_SPRINTS.md`）

| 项 | 状态 |
|----|------|
| A1 下架/停售/取消活动 | 已落地 API + 主办方台按钮 |
| A2 批量退款 | `BatchRefundEvent` + 取消活动联动 |
| A3 排队体验 | OrderDetail 自动轮询 + 横幅 |
| A4 清零食前端遗留 | 已删未引用 views/admin/cart；后端 snack 代码仍归档 |
| B1 票额对账 | `TicketCompensationService` 5min |
| B2 Outbox | `004_ticket_order_outbox` + `StartOutboxPublisher` |
| B3 限流/压测/指标 | 写路径 IP 限流；`ticket_smoke`/`ticket_rush_spike`；OrdersCreated/MQ/Compensation |
| C1 核销强化 | session_id 约束 + 今日统计 |
| C2 支付适配层 | `PaymentGateway` / `BalancePaymentGateway` |
| C3 密钥轮换 | `ticket_qr.previous_secrets` 多密钥验签 |

### 仍开放 / 建议下轮

1. **抢票并发集成测试**：同幂等键 + 换 token 双 Lua。
2. **限时开售多数量 UI**：`per_user_limit > 1`。
3. **后端 snack 死代码删除**：`product_*` / `seckill_*` / snack `order_*`（确认无回滚需求后）。
4. **真实支付渠道**：在 `PaymentGateway` 上接微信/校园支付。

## 协作规则

1. **小步可逆**：一次只推进一条链路；不引入新框架。
2. **钱与票额**：MySQL 为最终事实；Redis 是前置闸。任何「先扣 Redis」路径必须在失败/幂等命中时显式回滚。
3. **购票信息**：普通单与抢票单共用 `validatePurchaseInfo`；实名活动不得跳过观演人。
4. **改并发/库存后**：至少跑  
   `go test ./service/ ./models/ -count=1`  
   能起依赖时再验：下单 → Redis → MQ → 支付 → 核销。
5. **文档**：改行为时同步更新本文件「当前状态」表；大阶段目标仍写在 `FUCHANG_*.md`。
6. **提交**：未经用户明确要求，不 `git commit` / `push`。

## 关键文件索引

```text
backend/service/ticket_order_service.go   # 普通单、消费、支付、超时、归还
backend/service/rush_sale_service.go      # 令牌、Lua、抢票建单
backend/service/ticket_verification_service.go
backend/service/ticket_catalog_service.go
frontend/src/views/RushSales.vue          # 抢票确认购票信息
frontend/src/views/Checkout.vue           # 普通购票
docs/FUCHANG_PHASE1.md
docs/FUCHANG_ADMISSION_TICKET.md
docs/FUCHANG_ORGANIZER_CLOSURE.md
docs/FUCHANG_COLLAB.md                    # 本文件
```

## 变更日志

- **2026-07-22**：验收 IT 栈；活动封面已有 Unsplash 图；重建 `15173` 前端含用户向信任文案；新增 `tests/integration/rush_idempotency.mjs`（同幂等键换 token 通过）。
- **2026-07-21 Cursor**：完成 review 所列 P1/P2 修复；抢票 API 增加购票信息；列表返回 `real_name_required` / `event_title`；新增本协作文档；落地 Sprint A/B/C 最小切片。
