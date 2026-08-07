# 多热点抢票压测扫描：multi-hotspot-buckets-workers-8-valid-1500-20260731-h2

- TOTAL_ORDERS=1500
- HOTSPOT_STEPS=2
- RUNS=3
- buckets_enabled=true

| 热点数 | 入口 QPS 中位 | execute p99 中位 | MQ 追平中位 | row_lock_waits 中位 | 成功率中位 |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 2 | 468.31 | 184.53ms | 7.4s | 368 | 1 |

