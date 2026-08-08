# Gofun 票务集成测试

该环境只用于一次性验证 MySQL、Redis、RabbitMQ、Elasticsearch 与后端的真实写链路，不复用开发数据库。

## 启动与支付/核销 E2E

```powershell
docker compose -p gofun-it `
  -f tests/integration/docker-compose.ticketing.yml `
  up -d --build --wait

$env:BASE_URL='http://127.0.0.1:18080/api/v1'
node tests/integration/payment_verification_e2e.mjs
```

该脚本会自动创建主办方、场次与票档，验证：

1. 下单后进入 `pending_payment`，支付沙箱只返回 `pending`，异步回调成功后才出票；
2. `failed` 回调不会出票，订单标记 `payment_status=failed`；
3. 电子票凭证能被主办方核销，重复核销返回 `already_used`；
4. 无效签名的支付回调被拒绝。

## 其他并发测试

```powershell
$env:BASE_URL='http://127.0.0.1:18080/api/v1'
node tests/integration/rush_concurrency.mjs
node tests/integration/rush_idempotency.mjs
```

## 服务地址

| 服务 | 地址 |
| --- | --- |
| Backend | `127.0.0.1:18080` |
| MySQL | `127.0.0.1:13306` |
| Redis | `127.0.0.1:16379` |
| RabbitMQ AMQP | `127.0.0.1:25672` |
| RabbitMQ Management | `127.0.0.1:35672` |
| Elasticsearch | `127.0.0.1:19201` |

## 清理

```powershell
docker compose -p gofun-it `
  -f tests/integration/docker-compose.ticketing.yml `
  down -v --remove-orphans
```

`down -v` 只删除 `gofun-it` 的容器、网络和测试卷，不影响开发环境的 Compose 项目。
