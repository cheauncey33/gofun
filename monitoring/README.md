# Gofun 监控

先启动票务后端，再从仓库根目录启动监控栈：

```powershell
docker-compose -f monitoring/docker-compose.monitoring.yml up -d
```

- Grafana: `http://127.0.0.1:3000`（默认 `admin/admin`）
- Prometheus: `http://127.0.0.1:9090`
- 默认面板：`Gofun 票务核心链路`

核心面板包括：

- `ticket_order_consumer_transactions_total`：订单消费者事务成功/失败计数；库存提交后才会推进 `pending_payment`。
- Rush Execute P99：`POST /api/v1/rush-sales/:id/execute` 的 5 分钟窗口 p99。
- `mysql_innodb_row_lock_waits_total`：MySQL 全局 `Innodb_row_lock_waits`，面板展示 5 分钟增量。
- `mq_queue_ready_messages`：RabbitMQ 等待投递的 ready 消息数，作为本项目的实时 MQ backlog 指标。

Prometheus 默认抓取宿主机 `8080` 的开发后端和 `18080` 的集成后端；按实际启动方式保留对应 target 即可。
