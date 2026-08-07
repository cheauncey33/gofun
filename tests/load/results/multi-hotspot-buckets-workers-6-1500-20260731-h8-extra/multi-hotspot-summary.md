# 多热点抢票压测扫描：multi-hotspot-buckets-workers-6-1500-20260731-h8-extra

- TOTAL_ORDERS=1500
- HOTSPOT_STEPS=8
- RUNS=1
- buckets_enabled=true

| 热点数 | 入口 QPS 中位 | execute p99 中位 | MQ 追平中位 | row_lock_waits 中位 | 成功率中位 |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 8 | 391.5 | 237.59ms | 7.91s | 52 | 1 |

