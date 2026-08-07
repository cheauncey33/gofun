# 赴场票务集成测试环境

这套编排只用于跨 MySQL、Redis、RabbitMQ 的写链路验证，不复用开发环境的数据卷。

## 隔离范围

- Compose 项目名：`fuchang-it`
- Backend：`127.0.0.1:18080`
- Web 预览（单独启动前端容器时）：`127.0.0.1:15173`
- MySQL：`127.0.0.1:13306`
- Redis：`127.0.0.1:16379`
- RabbitMQ AMQP：`127.0.0.1:25672`
- RabbitMQ Management：`127.0.0.1:35672`
- Elasticsearch：`127.0.0.1:19201`（活动关键词检索；失败自动降级 MySQL LIKE）

配置中的密码是隔离测试专用固定值，不能用于开发或生产环境。
浏览器跨域允许来源仅包含本地开发端口 `5173` 和隔离预览端口 `15173`。

## 启动

```powershell
docker compose -p fuchang-it `
  -f tests/integration/docker-compose.ticketing.yml `
  up -d --build --wait
```

重建仅前端预览容器（宿主机 nginx 代理到 backend 容器）：

```powershell
docker build -t fuchang-it-frontend:latest -f frontend/Dockerfile .
docker rm -f fuchang-it-frontend-web
docker run -d --name fuchang-it-frontend-web `
  --network fuchang-it_default `
  -p 15173:80 `
  fuchang-it-frontend:latest
```

抢票幂等回归（需已有进行中的 campaign）：

```powershell
$env:BASE_URL='http://127.0.0.1:18080/api/v1'
$env:RUSH_CAMPAIGN_ID='1'
node tests/integration/rush_idempotency.mjs
```

抢票并发验收套件（自举 campaign，无需手工造数；面试证据优先跑这个）：

```powershell
$env:BASE_URL='http://127.0.0.1:18080/api/v1'
node tests/integration/rush_concurrency.mjs
```

库存分桶验收（需 `inventory.buckets_enabled=true`，查 MySQL 桶行数 + 全局限购）：

```powershell
$env:BASE_URL='http://127.0.0.1:18080/api/v1'
$env:MYSQL_CONTAINER='whu-snack-go-capacity-mysql-1' # 或 fuchang-it 的 mysql 容器名
node tests/integration/inventory_buckets.mjs
```

多热点抢票扫描（DB 直插多个活动/票档，不打同一热点）：见 `tests/load/ticket_rush_multi_hotspot.mjs` 与 `LOAD_TEST_PLAN.md` §6。


覆盖：

1. 同用户同幂等键 8 路并发 → 只有一单  
2. 同用户超限购并发 → 成功数 = `per_user_limit`  
3. 12 人抢 `total_quota=3` → 恰好 3 成功，其余 4xx  

如果希望在宿主机直接运行后端，可以改用：

```powershell
cd backend
go run . -config ../tests/integration/backend.ticketing.yaml
```

此时不要同时启动 Compose 中的 `backend` 服务。

## 必测链路

1. 创建主办方、场馆、活动、场次、票档并发布。
2. 普通购票重复提交相同 `X-Idempotency-Key`，订单 ID 必须相同。
3. 消费完成后 MySQL 与 `fuchang:ticket:stock:<tierID>` 必须一致。
4. 限时开售同幂等键重复提交只建一单；超过个人限购必须失败。
5. 待支付订单超时后，MySQL 与 Redis 票额必须同时归还。
6. 停止后端并注入 queued 订单，重启后只能扣减一次，三个 RabbitMQ 队列最终应清空。

## 清理

下面的命令会删除 `fuchang-it` 的容器、网络和独立测试卷：

```powershell
docker compose -p fuchang-it `
  -f tests/integration/docker-compose.ticketing.yml `
  down -v --remove-orphans
```

它不会删除其他 Compose 项目或开发环境数据卷。
