# 业务场景压测

从仓库根目录运行。使用独立 Docker 项目，故障测试会停止该项目的 RabbitMQ 并强制终止后端。不要连接生产环境。

```powershell
./tests/load/k6/run_full_chain_baseline.ps1 -Project gofun-scenarios-local -Rate 20 -Duration 2s -FastPrepare -K6InDocker -K6Image grafana/k6:0.57.0 -ResourceCompose tests/load/docker-compose.audit.yml -KeepStack -OutputDir tests/load/results/scenarios-warmup
node tests/load/k6/run_business_scenarios.mjs gofun-scenarios-local tests/load/results/scenarios-local
```

需要 Docker 和 Node.js；本机 18580、13327、16400、26073、36073、19602 端口空闲。初始化脚本构建后端、迁移数据库并准备基础数据。场景运行器固定访问 18580，每个场景创建独立活动。

| 场景 | 输入 | 验收 |
|---|---|---|
| 售罄 | 库存 1,000，提交 5,000 次 | 确认 1,000 单；其他返回售罄；库存不为负 |
| 重复下单 | 100 个用户，各重复同一请求 10 次 | 仅 100 单；每用户库存扣一次；同键改数量被拒绝 |
| 限购 | 一用户 200 个不同请求，限购 20 | 确认 20 单，其他返回限购 |
| 全局/IP 限流 | 分别配置 50/s、burst 50，输入 200/s 持续 5 秒 | 放行和 429 分开统计，放行不超额度容差 |
| 用户滑窗限流 | 配置 10 次/秒，输入 200/s 持续 5 秒 | 返回 429；放行不超额度容差 |
| MQ 中断 | 停止 Broker，输入 100/s 持续 10 秒，再恢复 | 故障期间受理订单处于 queued；恢复后全部确认 |
| 应用重启 | 停止 Broker，输入 100/s 持续 5 秒，再强杀应用并恢复 | 已受理订单恢复后全部确认 |
| 混合流量 | 100 个业务动作/秒持续 60 秒 | 查询、登录、注册、普通下单、抢购等逐项统计；业务成功率 100% |

限流场景提高其他限流层的阈值，重复使用同一幂等键，避免库存和限购干扰。放行数包含幂等响应，不表示新建订单数。混合流量使用真实密码，并为每个用户预置订单以覆盖订单详情；一个动作可能发起多个 HTTP 请求。

每个场景结束后核对订单状态、Consumer Inbox、失败 Outbox、MySQL/Redis 每个库存桶及用户限购。任何业务错误、库存核对失败、延迟阈值超限或压测端丢弃迭代都会使运行器失败。输出目录包含 report.md、summary.json 和各场景的 k6 统计、每秒指标采样及日志。

已有前面场景的 summary.json 时，追加参数 `mixed` 可只重跑混合场景；会创建新的活动并替换该场景的统计。

这是短时功能与负载验证，不代表持续容量上限；支付与超时竞争、Redis 数据丢失、多应用实例故障需要另外验证。

## 阶梯与前后对照

```powershell
./tests/load/k6/run_staircase.ps1
```

需要已构建的历史基线与当前镜像，可通过 `-BaselineImage`、`-CurrentImage` 指定。默认使用本次保留的两个镜像；镜像 ID 写入 images.json。运行独立的 `gofun-staircase-*` 项目，前述端口必须空闲。

按 100、200、300、400 单/秒分别测两个版本，每档 120 秒，最后复测 200 档。顺序交替 AB/BA，每轮排空后开始下一轮。基线 6 Consumer、当前 12 Consumer，其他资源预算相同；测试整组优化效果。

analyze_staircase.mjs 在每轮完成后更新 report.md 和 analysis.json。持续吞吐使用去除启动预热与尾部的 HTTP 采样窗口；受理至确认延迟使用完整排空后的直方图。报告分别列出 HTTP P99、确认 P99、未确认订单增长、连接池等待和事务阶段耗时。高档超出容量条件时仍保留结果并核对所有订单与库存，不能把最终排空当作容量通过。所有档位完成后停止测试栈并保留数据。

完成后停止独立测试栈，保留数据供复查：

```powershell
docker compose -p gofun-scenarios-local -f tests/integration/docker-compose.ticketing.yml -f tests/load/docker-compose.capacity.yml -f tests/load/docker-compose.audit.yml stop
```
# 下单限流与削峰验证

`powershell -NoProfile -ExecutionPolicy Bypass -File tests/load/k6/run_peak_recovery.ps1`

使用独立测试环境，先测下单共用令牌桶 `order_rate: 150`、`order_burst: 50`，再放宽该入口配额验证 MQ 缓冲。普通下单与抢票共用该配额；现有全局、IP、用户写限流独立生效。单机令牌桶的总额度按实例计算。

每轮输入为 150/s 持续 120 秒、500/s 突发 10 秒、50/s 持续 60 秒，随后停止输入等待清空；按阶段分别统计受理数量、429 数量及成功受理的 P95/P99，并核对最终订单、Inbox、库存和看板。脚本保留数据库持久化配置，完成后停止测试容器，结果目录包含原始采样和 `report.md`。限流轮验证容量保护，放宽配额轮验证削峰，不能把主动拒绝解释成 MQ 吸收了全部高峰流量。
# 持续压测

`powershell -NoProfile -ExecutionPolicy Bypass -File tests/load/k6/run_soak.ps1`

独立环境运行 25 分钟，150/s 为基础输入，第 10 分钟插入 500/s 的 10 秒高峰，随后恢复到 150/s。入口限流放宽以观察后台缓冲；使用 5 万用户、50 万库存和 60 分钟订单超时，保留每用户限购 20。Consumer、Publisher、连接池和数据库持久化设置保持不变。

每分钟记录受理/确认吞吐、成功受理 HTTP P99、待确认峰值、事务/提交耗时和连接池等待；`live.ndjson` 另存容器资源与 MySQL I/O 快照。执行过程中可运行 `node tests/load/k6/analyze_soak.mjs <结果目录> --live` 查看当前数据。结束后核对订单、Inbox、库存和看板并生成 `report.md`，停止测试容器、保留数据卷。
