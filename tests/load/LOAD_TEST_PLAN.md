# Gofun 票务压测计划

本目录只保留当前票务链路的压测入口。脚本会写入真实订单和库存，必须使用专用
MySQL、Redis 和 RabbitMQ；不要拿开发数据做写场景。

## 当前范围

1. ticket_smoke.mjs：登录、活动浏览、普通购票和订单链路冒烟。
2. ticket_rush_spike.mjs：单热点抢票 execute 压测。
3. ticket_rush_staircase.mjs：并发阶梯，定位吞吐和延迟拐点。
4. ticket_rush_wave_sweep.mjs：固定订单量下扫描 WAVE_SIZE。
5. ticket_rush_profile.mjs：采集 pprof、Prometheus 快照和结果清单。
6. tests/integration/rush_concurrency.mjs、rush_idempotency.mjs：
   验证限购、幂等、不超卖和异步追平。
7. tests/load/k6/：固定 VU 的入口吞吐与 p99 压测。

历史结果保存在 tests/load/results/，只代表对应日期、机器和配置，不代表生产
容量承诺。压测报告必须同时记录成功率、p99、队列追平时间、MySQL 锁等待、
Redis 和 RabbitMQ 资源。

核心结果统一按 `docs/PERFORMANCE_CORE.md` 执行：固定同一场景跑 3 轮，使用
Prometheus Histogram 的本轮增量计算入口、受理到可支付、Consumer 事务 p99，
并用 `render_core_performance_report.mjs` 生成同一份报告。其他分析脚本只用于定位
瓶颈，不进入简历或面试中的核心性能结论。

## 测试前准备

修改 backend/config/config.yaml 后重启后端。容量测试可以暂时放宽限流：

```yaml
ratelimit:
  global_rate: 100000
  global_burst: 100000
  ip_rate: 100000
  ip_burst: 100000
```

常用环境变量：

```powershell
$env:BASE_URL='http://127.0.0.1:8080/api/v1'
$env:CONCURRENCY='100'
$env:DURATION_SECONDS='60'
$env:THINK_MS='0'
$env:ADMIN_USERNAME='admin'
$env:ADMIN_PASSWORD='admin123'
$env:LOAD_USER_PREFIX='load_user_'
$env:LOAD_PASSWORD='123456'
```

恢复真实限流后再做一轮小规模策略验证。THINK_MS=0 用于测入口吞吐，
非零值用于模拟用户节奏。

## 推荐执行顺序

### 1. 冒烟

```powershell
node tests/load/ticket_smoke.mjs
```

确认活动可浏览、普通购票请求可进入异步链路，订单最终落为
pending_payment 或按支付结果进入后续状态。

### 2. 普通购票压力

```powershell
$env:CONCURRENCY='50'
$env:DURATION_SECONDS='120'
node tests/load/ticket_smoke.mjs
```

观察 API 延迟、Outbox 发布、消费者处理、订单状态和票档 quota，确认 RabbitMQ
追平后 Redis 与 MySQL 收敛。

### 3. 抢票阶梯与波次

```powershell
$env:RUSH_CAMPAIGN_ID='<campaign id>'
$env:STEPS='50,200,500,1000,1500'
$env:WAVE_SIZE='50'
node tests/load/ticket_rush_staircase.mjs
node tests/load/ticket_rush_wave_sweep.mjs
```

验收：不超卖、单用户不超过限购、重复幂等不重复建单、失败预扣能回滚、
队列最终追平。入口 p99 和成功率必须与同一组机器、配置和数据规模一起解释。

### 4. 集成正确性

```powershell
node tests/integration/rush_concurrency.mjs
node tests/integration/rush_idempotency.mjs
node tests/integration/inventory_buckets.mjs
```

支付、电子票和核验链路使用 tests/integration/payment_verification_e2e.mjs，
不要把它与高并发压测混在同一个环境中。

## 运行中记录

- 脚本输出：请求总数、成功率、QPS、p50/p90/p95/p99、错误分类。
- 后端：/metrics、日志、pprof（只绑定 loopback）。
- MySQL：连接数、慢查询、锁等待和死锁。
- Redis：ops/sec、延迟、内存和 quota key。
- RabbitMQ：ready/unacked、发布/消费数量和最终追平时间。
- 主机：CPU、内存、网络端口和 Docker 资源。

## 结果解释

- 所有请求成功只说明本轮场景和数据规模通过，不等于系统最大容量。
- 业务限购导致的 4xx 要与 5xx、429、网络错误分开统计。
- 入口吞吐上升但 p99 恶化时，优先检查限流、Redis、MySQL 锁和本机资源。
- 测试结束后等待 MQ 追平，并核对订单、票档 quota、支付状态和电子票数量。
