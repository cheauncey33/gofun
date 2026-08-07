# 多热点抢票压测扫描：multi-hotspot-buckets-workers-8-valid-1500-20260731-h1

- TOTAL_ORDERS=1500
- HOTSPOT_STEPS=1
- RUNS=3
- buckets_enabled=true

| 热点数 | 入口 QPS 中位 | execute p99 中位 | MQ 追平中位 | row_lock_waits 中位 | 成功率中位 |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 445.12 | 262.57ms | 7.92s | 612 | 1 |

