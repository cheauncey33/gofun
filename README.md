# 赴场

> 赴热爱之场，见想见的人。

赴场是一个使用 Go 构建的多主办方活动票务平台。当前第一阶段聚焦无选座票务：
主办方维护活动、场馆、场次和票档，用户完成普通购票或限时开售抢票，
Redis 负责前置票额与并发控制，RabbitMQ 异步确认订单，MySQL 保存最终订单和票额。

## 技术栈

- 后端：Go、Gin、GORM、MySQL、Redis、RabbitMQ、Snowflake ID
- 前端：Vue 3、Vite、Element Plus、Vue Router、Axios
- 可观测性：Prometheus `/metrics`、Grafana

## 第一阶段能力

- 多主办方与 `owner / operator` 成员权限
- 场馆、活动、场次、票档管理
- 主办方工作台：运营概览、活动创建/续配/发布、近期订单
- 公开活动发现与活动详情
- 普通购票：Redis 预扣、RabbitMQ 异步确认、幂等请求
- 限时开售：一次性令牌、活动票额、底层票额、个人限购 Lua 原子校验
- 订单状态：`queued → pending_payment → paid / cancelled`
- 超时取消、票额释放、余额支付和退款
- 支付后按购买数量签发电子票，订单详情展示独立二维码
- 主办方扫码或手动输入票码核销，并保留成功与失败审计记录
- 服务重启时按 queued 订单重建 Redis 并重投 RabbitMQ

本阶段不包含在线选座、实名观演人、电子票转赠、离线核销、第三方支付和主办方结算。
完整的范围、字段、接口与风险见 [第一阶段改造说明](docs/FUCHANG_PHASE1.md)。
主办方可运营闭环见 [主办方工作台说明](docs/FUCHANG_ORGANIZER_CLOSURE.md)。
电子票、核销与数据库基线见 [电子票与核销说明](docs/FUCHANG_ADMISSION_TICKET.md)。

## 本地运行

后端：

```powershell
cd backend
go run . -config ./config/config.yaml
```

前端：

```powershell
cd frontend
npm.cmd install
npm.cmd run dev
```

验证：

```powershell
cd backend
go test ./... -count=1

cd ../frontend
npm.cmd run build
```

## 重要一致性约定

- MySQL 是票务订单和票额的最终事实来源。
- Redis 键使用 `fuchang:ticket:*` 与 `fuchang:rush:*` 命名空间。
- RabbitMQ 队列使用 `fuchang.order.*`。
- 新票务金额一律使用 `int64` 分；旧零食领域的 `float64` 字段只为回滚保留，未挂载运行路由。
- `queued` 表示订单凭据已经创建，但消费者尚未完成 MySQL 票额确认；它不是支付成功。

## 源码保护

改造前源码已保存为本地标签 `pre-fuchang-phase1-20260721`，当前改造分支为
`feature/fuchang-phase1`。离线恢复包位于被 Git 忽略的 `.backup/` 目录。
