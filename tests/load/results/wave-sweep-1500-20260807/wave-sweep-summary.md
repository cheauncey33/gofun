# WAVE_SIZE 扫档（STEPS=1500）

- 标签：`wave-sweep-1500-20260807`
- 公式：`success_qps = success_orders / execute_seconds`
- 发压：按波串行，每波 `WAVE_SIZE` 并发 `rush execute`

| WAVE_SIZE | 成功单数 | execute 秒 | success_qps | execute p99 | MQ 追平 |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 50 | 1500 | 2.62 | 571.77 | 120.84ms | 8.95s |
| 200 | 1500 | 2.04 | 734.77 | 351.98ms | 16.74s |
| 500 | 1500 | 1.72 | 871.93 | 635.79ms | 15.61s |
| 1000 | 1500 | 1.4 | 1072.63 | 911ms | 19.64s |

## 解读提示

- 若 QPS 随 WAVE 明显上升：此前被客户端 wave 卡住，不是服务端天花板。
- 若 WAVE 增大后 QPS 平台/下降、p99 恶化：接近入口瓶颈（限流/Redis/本机 Docker）。

