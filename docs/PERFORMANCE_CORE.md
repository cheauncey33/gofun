# 核心链路性能口径

本项目只对当前抢票下单链路保留一套核心性能口径，避免把不同脚本、不同组件和
不同时间窗口的 p99 混在一起。

## 固定指标

| 指标 | 数据源 | 时间边界 | 作用 |
| --- | --- | --- | --- |
| HTTP 吞吐、p99 | `http_request_duration_seconds`，限定 `POST /api/v1/rush-sales/:id/execute` | 服务端收到请求到返回 | 判断入口承载和用户提交延迟 |
| 受理到可支付 p99 | `ticket_order_accepted_to_pending_payment_duration_seconds` | 订单任务创建到首次进入 `pending_payment` | 覆盖 Outbox、RabbitMQ 排队和 Consumer 落库 |
| Consumer 事务 p99 | `ticket_order_consumer_transaction_duration_seconds{result="success"}` | 单条消息的 MySQL 最终化事务 | 定位异步落库瓶颈 |
| MQ 追平时间 | 压测脚本每 2 秒读取 RabbitMQ 与 MySQL | HTTP 停止到工作队列、待发布 Outbox 连续归零 | 衡量流量结束后的消化能力 |

前三个延迟统一使用 Prometheus Histogram。压测开始前取一次累计值，MQ 追平后
再取一次，用两次快照的 bucket 增量计算本轮 p99。它不是另起协程定时估算；采样
协程只负责保存累计指标和队列状态。

## 固定执行规则

1. 使用专用 MySQL、Redis、RabbitMQ，不与开发数据混用。
2. 同一场景固定 VUS、持续时间、Consumer 数、库存分桶数、配额和 Docker 资源。
3. 每个场景完整运行 3 轮；单轮必须等工作队列和待发布 Outbox 归零。
4. 报告保留逐轮结果，最终值取三轮中位数，不从不同日期的历史报告拼接指标。
5. HTTP 成功率必须为 100%，工作队列、待发布 Outbox、死信必须为 0；否则该轮
   只能用于故障分析，不能作为性能结论。

完整环境运行单轮后，会得到 `run-summary.json` 和 `lifecycle-samples.json`。三轮
使用相同参数运行，然后统一生成报告：

```powershell
node tests/load/render_core_performance_report.mjs `
  --output tests/load/results/core-performance-report.md `
  tests/load/results/core-round-1/vus-100 `
  tests/load/results/core-round-2/vus-100 `
  tests/load/results/core-round-3/vus-100
```

报告只代表当次机器、配置和数据规模。入口请求成功不等于订单已经写入 MySQL；
必须同时解释“受理到可支付 p99”和 MQ 最终追平结果。
