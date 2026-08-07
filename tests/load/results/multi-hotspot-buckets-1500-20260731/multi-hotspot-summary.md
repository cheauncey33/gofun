# 多热点抢票压测扫描：multi-hotspot-buckets-1500-20260731

- TOTAL_ORDERS=1500
- HOTSPOT_STEPS=1,2,4,8
- RUNS=3
- buckets_enabled=true

| 热点数 | 入口 QPS 中位 | execute p99 中位 | MQ 追平中位 | row_lock_waits 中位 | 成功率中位 |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 1 | 573.7 | 174.61ms | 10.95s | 276 | 1 |
| 2 | 675.01 | 94.47ms | 11.61s | 779 | 1 |
| 4 | 726.71 | 104.51ms | 11.44s | 288 | 1 |
| 8 | 663.09 | 127.05ms | 10.22s | 42 | 1 |

