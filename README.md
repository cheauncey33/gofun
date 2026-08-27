# Gofun · 多主办方活动票务平台

Gofun 是一个以 Go 为后端、Vue 3 为前端的活动票务系统。当前同时支持无座票和在线选座，覆盖主办方入驻与审核、活动发布、普通购票、限时抢票、候补、支付沙箱、电子票和现场核销。

## 核心链路

```text
普通购票 / 限时抢票 / 选座
→ 写限流与幂等校验
→ Redis Lua 预扣票额（选座订单同时锁定具体座位）
→ MySQL 事务写 queued 订单与 Outbox
→ RabbitMQ 异步消费
→ pending_payment
→ 支付成功签发电子票 / 超时或取消释放票额与座位
→ WebSocket 推送订单状态
```

MySQL 是订单、支付、电子票和库存的最终事实来源；Redis 是入口并发闸门。Outbox publisher confirm、消费者幂等、库存恢复凭证和周期性补偿共同处理异常与重试。

## 当前能力

| 角色 | 已实现能力 |
|------|------------|
| 用户 | 活动浏览、无座购票、在线选座、抢票、候补、订单支付/取消/退款、电子票查看 |
| 主办方 | 入驻申请、场馆/活动/场次/票档管理、座位图配置、送审与撤回、停售/取消、批量退款、订单与核销、经营漏斗 |
| 平台管理员 | 主办方审核、活动审核、平台健康与业务概览 |
| 系统 | Redis 预扣、RabbitMQ 削峰、事务 Outbox、支付超时、库存补偿、WebSocket、Prometheus/Grafana |

候补采用按提交顺序排队的独立状态链路；退款释放的无座票可优先派给已付款候补。选座订单维护 `available → held → sold` 状态，并在取消、超时和退款时释放座位。

## 技术栈

- 后端：Go、Gin、GORM、MySQL、Redis、RabbitMQ、Snowflake ID
- 前端：Vue 3、Vite、Element Plus、Vue Router、Axios
- 可观测性：Prometheus `/metrics`、Grafana、OpenTelemetry、pprof
- 验证：Go 单元/集成测试、Node 业务冒烟脚本、k6 压测

## 本地运行与验证

```powershell
cd backend
go run . -config ./config/config.yaml
go test ./... -count=1

cd ../frontend
npm.cmd install
npm.cmd run dev
npm.cmd run build
```

完整依赖可从 `deploy/env.example` 创建本地 `.env` 后使用 Docker Compose 启动。`backend/config/config.yaml` 是本地配置，不应作为共享环境配置直接提交；共享默认值维护在 `backend/config/config.example.yaml`。

## 生产化边界

- 当前支付只实现内置沙箱，不代表已经接入微信、支付宝等真实渠道。
- 真实渠道仍需补充主动查询、对账、退款重试和重启恢复。
- 运行时全链路验收需要独立的 MySQL、Redis 和 RabbitMQ；写压测会修改真实数据。
- 压测原始产物默认不提交，结论应整理进文档并标明配置、日期与边界。

## 文档导航

- [当前票务范围](docs/FUCHANG_PHASE1.md)
- [一致性设计](docs/CONSISTENCY.md)
- [支付沙箱](docs/FUCHANG_PAYMENT_SANDBOX.md)
- [电子票与核销](docs/FUCHANG_ADMISSION_TICKET.md)
- [性能与压测](docs/PERFORMANCE.md)
- [当前协作状态](docs/FUCHANG_COLLAB.md)
