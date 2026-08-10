# k6 抢票入口压测

用 k6 压 **同步入口** `POST /rush-sales/:id/execute`，专门看标准 QPS / p99。  
业务正确性（成功数=库存、MQ 追平）仍用现有 Node 脚本：`ticket_rush_staircase.mjs` / `ticket_rush_wave_sweep.mjs`。

## 分工

| 工具 | 职责 |
|------|------|
| Node 脚本 | 建活动、验不超卖、采资源、MQ 追平 |
| **k6** | 恒定/阶梯 VU 发压，报 http_req_rate、p50/p90/p99 |

## 依赖

```powershell
winget install GrafanaLabs.k6
k6 version
```

## 1. 起压测栈

```powershell
cd D:\newProgram\gofun
$env:INVENTORY_BUCKETS_ENABLED='true'
docker compose -p gofun-capacity `
  -f tests/integration/docker-compose.ticketing.yml `
  -f tests/load/docker-compose.capacity.yml up -d
```

健康检查：`http://127.0.0.1:18080/healthz`

## 2. 准备夹具（Node）

```powershell
$env:BASE_URL='http://127.0.0.1:18080/api/v1'
$env:K6_USERS='500'
$env:RUSH_TOTAL_QUOTA='100000'   # 大库存，避免入口很快卖光干扰吞吐
$env:RUSH_PER_USER_LIMIT='20'    # 活动单笔上限 20；吞吐靠多用户轮转
node tests/load/k6/prepare_rush_fixture.mjs
```

写出：`tests/load/k6/fixtures/rush_execute.json`（含 JWT，勿提交）。

> 说明：票务活动 `max_tickets_per_order` 最大 20，因此单用户限购不能无限放大。k6 脚本按 `__ITER` 轮转用户，请准备足够用户（建议 ≥ 最大 VU × 预估每 VU 请求数 / 20）。

## 3. 跑 k6

恒定 50 VU / 30s：

```powershell
k6 run -e VUS=50 -e DURATION=30s tests/load/k6/rush_execute.js
```

拉高并发：

```powershell
k6 run -e VUS=200 -e DURATION=30s tests/load/k6/rush_execute.js
k6 run -e VUS=500 -e DURATION=30s tests/load/k6/rush_execute.js
k6 run -e VUS=1000 -e DURATION=30s tests/load/k6/rush_execute.js
```

阶梯 VU（单次脚本内 ramp）：

```powershell
k6 run -e RAMP_VUS=500 -e RAMP_UP=15s -e HOLD=30s -e RAMP_DOWN=10s tests/load/k6/rush_execute.js
```

峰值扫档（每档重新 prepare + 多档 VUS）：

```powershell
$env:BASE_URL='http://127.0.0.1:18080/api/v1'
$env:PEAK_VUS='50,100,200,500,1000'
$env:PEAK_DURATION='20s'
$env:RUN_LABEL='k6-peak-$(Get-Date -Format yyyyMMdd-HHmmss)'
node tests/load/k6/run_peak_sweep.mjs
```

摘要目录：`tests/load/results/<RUN_LABEL>/peak-summary.md`

## 4. Docker network mode

Windows 上的 k6 通过 `127.0.0.1:宿主机端口` 压测时，会经过 Docker Desktop 的端口转发。高 VU 测试建议让 k6 运行在 Compose 网络内，直接访问 `http://backend:8080`：

```powershell
docker pull grafana/k6:latest
powershell.exe -NoProfile -ExecutionPolicy Bypass -File `
  tests/load/k6/run_full_chain_baseline.ps1 `
  -Project gofun-k6docker `
  -Vus 100,200,500 `
  -Duration 20s `
  -DrainSeconds 720 `
  -FastPrepare `
  -K6InDocker
```

`-K6InDocker` 会自动把 k6 加入 `${Project}_default` 网络，并使用 `http://backend:8080/api/v1`；默认不加该开关时，仍使用 Windows 本机 k6 和宿主机端口。

## 读数口径

- **http_req_rate**：k6 观测到的请求吞吐（更接近标准入口 QPS）
- **http_req_duration p99**：入口延迟
- **rush_execute_success**：HTTP 200 业务成功比例（库存大时接近 1）
- 库存打光 / 个人限购打满后会出现 400/409，属预期，不代表系统错误

### 本机对照（2026-08-07，capacity 单实例 + 分桶）

新夹具、`VUS=50`、`DURATION=15s`、大库存：

| 指标 | 值 |
|------|-----|
| http_req_rate | **~925 req/s** |
| p99 | **~153ms** |
| 业务成功率 | **100%** |

## 注意

1. 夹具里的 JWT 会过期，压测前重新 `prepare_rush_fixture.mjs`。
2. **每轮干净吞吐测试请重新 prepare**（换新 campaign）；复用已使用夹具容易先撞上个人限购。
3. capacity 栈已把写限流配额拉高；若用普通配置，429 会先打满。
4. 不同发压模型的数字不能直接横比，必须同时记录请求模型、夹具、限购和数据规模。
