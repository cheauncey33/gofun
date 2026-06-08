# 大厂后端面经来源与频次统计

## 依据来源

- 公开搜索结果、可访问网页标题和已核验 URL
- 本文件区分“已访问标题/页面”和“待全文核验”
- 严格频次统计需要逐篇阅读全文；当前 Top100 是基于已收集来源主题的初版优先级清单，不伪装成精确词频

## 来源索引

| 序号 | 状态 | 来源 | 主题 | URL |
| --- | --- | --- | --- | --- |
| 1 | 已访问 | 面灵面经聚合 | 后端、Redis、MySQL、MQ、并发 | https://mj.mianlingai.com/ |
| 2 | 已访问 | 面灵: 字节后端面经 | 项目、Redis 防超卖、MySQL、MQ | https://mj.mianlingai.com/interview/bytedance-java-backend-interview-2830536/ |
| 3 | 已访问 | Go 技术论坛 | Go/PHP 后端面经、Redis | https://learnku.com/articles/86574 |
| 4 | 已访问 | 面试鸭 Go 题库 | Go 高频题、Redis、MySQL | https://www.mianshiya.com/?category=go |
| 5 | 已访问 | 帅地: 字节 Go 高频题 | Go、MySQL、Redis | https://www.iamshuaidi.com/3584.html |
| 6 | 已访问 | 帅地: 腾讯 Go 面经汇总 | Go 后端、腾讯面经 | https://www.iamshuaidi.com/2885.html |
| 7 | 已访问 | 博客园 Golang 后端研发面试笔记 | Go、网络、OS、MySQL、Redis、MQ | https://www.cnblogs.com/tmnhs/articles/17399044.html |
| 8 | 已访问 | Go 语言中文网后端考点 | 高并发、Redis、MQ | https://studygolang.com/articles/37350 |
| 9 | 已访问 | 牛客: 字节后端面经 | 项目、Redis、MQ | https://www.nowcoder.com/discuss/732234187624697856 |
| 10 | 已访问 | 脉脉: 阿里 Java 后端 | Redis 大 key、MySQL、RocketMQ、项目幂等 | https://maimai.cn/article/detail?efid=Ba1fhcvc38EuxMgVAWmZDg&fid=1885619951 |
| 11 | 已访问 | 卡码笔记腾讯云面经 | Java 高频、Redis、MySQL | https://notes.kamacoder.com/interview/java/tencent-cloud-interview.html |
| 12 | 已访问 | 高并发的哲学原理 PDF | 秒杀、队列削峰、MySQL/Redis 一致性 | https://pphc.lvwenhan.com/PPHC.pdf |
| 13 | 已访问 | 大厂面经 PDF | 后端综合、MQ、Redis | https://file.x2boot.com/%E5%A4%A7%E5%8E%82%E9%9D%A2%E7%BB%8F.pdf |
| 14 | 已访问 | CSDN/AtomGit 腾讯 Go 题 | GMP、MySQL B+ 树、MQ 削峰 | https://gitcode.csdn.net/69eeca2c0a2f6a37c5a64684.html |
| 15 | 已访问标题 | Go 语言中文网: 腾讯后端面经 | 腾讯、Go、MySQL、Redis、网络 | https://studygolang.com/articles/19527 |
| 16 | 已访问标题 | CSDN: 腾讯 Golang 后端面试题 | Golang、腾讯、MySQL、Redis、并发 | https://blog.csdn.net/launch2020/article/details/107390502 |
| 17 | 已访问标题 | 博客园: BAT 面经 | 字节、阿里、腾讯、项目、基础 | https://www.cnblogs.com/yeya/p/15845544.html |
| 18 | 待全文核验 | 牛客 Golang 面经 | Go、并发、项目 | https://www.nowcoder.com/discuss/145338 |
| 19 | 待全文核验 | go_interview GitHub 题库 | Go 高频题库 | https://github.com/go-share-team/go_interview |
| 20 | 待全文核验 | Redis 高频面试题汇总 | Redis 基础与场景题 | https://zhuanlan.zhihu.com/p/356284632 |
| 21 | 待全文核验 | 小林 coding 后端面试真题汇总 | 腾讯、字节、阿里、美团、京东等 | https://xiaolincoding.com/backend_interview/ |
| 22 | 待全文核验 | 小林 coding 图解网络 | TCP/HTTP/网络基础 | https://xiaolincoding.com/network/ |
| 23 | 待全文核验 | 小林 coding MySQL | MySQL、索引、事务、日志 | https://xiaolincoding.com/mysql/ |
| 24 | 待全文核验 | 小林 coding Redis | Redis 数据结构、缓存、持久化 | https://xiaolincoding.com/redis/ |
| 25 | 待全文核验 | JavaGuide 面试突击 | MySQL、Redis、MQ、并发、项目 | https://javaguide.cn/ |

## 高频主题初版

| 优先级 | 主题 | 频次判断 | 与本项目映射 |
| --- | --- | --- | --- |
| P0 | 项目深挖 | 几乎必问 | 整套文档核心 |
| P0 | Redis 缓存与扣库存 | 高频 | 商品 L1/L2 缓存、库存 key、Lua |
| P0 | MySQL 索引/事务/MVCC | 高频 | 订单事务、条件扣库存、索引字段 |
| P0 | MQ 可靠性/重复消费/积压 | 高频 | RabbitMQ ack/nack、幂等、prefetch |
| P0 | Go 并发/GMP/channel/context | 高频 | consumer worker、优雅停机、限流 map |
| P0 | 高并发秒杀/防超卖 | 高频 | Redis Lua + MQ + MySQL 兜底 |
| P1 | 网络/HTTP/TCP | 中高频 | Gin、HTTP 请求、CORS、连接 |
| P1 | 操作系统/Linux | 中高频 | 优雅停机、进程信号、部署排查 |
| P1 | 分布式一致性/幂等 | 高频 | 订单 ID 幂等、补偿、最终一致 |
| P1 | 压测与监控 | 项目相关高频 | Node 压测、Prometheus、MQ backlog |

## 优先背诵 Top 100 初版

### 项目深挖 1-20

1. 介绍一下你的 Campus-Mall 项目。
2. 项目的整体架构是什么？
3. 你独立完成了哪些模块？
4. 普通下单链路怎么走？
5. 秒杀链路怎么走？
6. 普通下单和秒杀有什么区别？
7. 为什么要把库存预热到 Redis？
8. Redis 库存和 MySQL 库存不一致怎么办？
9. 为什么 HTTP 返回成功时订单可能还没落库？
10. 如何设计订单状态查询？
11. 项目一共几张表？每张表做什么？
12. 订单表和订单项表为什么拆开？
13. 秒杀订单表为什么单独建？
14. 商品库存字段和秒杀库存字段是什么关系？
15. 项目里最有挑战的部分是什么？
16. 项目目前最大的不足是什么？
17. 如果上线生产，第一步补什么？
18. 如何证明你的优化有效？
19. 这个项目如何扩展到多实例？
20. 如果面试官质疑“高并发”，你怎么量化说明？

### Go 21-35

21. goroutine 和线程区别是什么？
22. GMP 模型是什么？
23. channel 底层和阻塞场景是什么？
24. select 如何工作？
25. context 的作用是什么？
26. 如何优雅停止 goroutine？
27. WaitGroup 使用注意点是什么？
28. mutex 和 RWMutex 区别是什么？
29. Go map 为什么并发不安全？
30. sync.Map 适合什么场景？
31. defer 执行顺序和性能注意点？
32. panic/recover 怎么用？
33. Go GC 三色标记是什么？
34. 内存逃逸是什么？
35. Go 服务如何排查 goroutine 泄漏？

### Redis 36-50

36. Redis 为什么快？
37. Redis 单线程和 I/O 多路复用是什么？
38. Redis 基本数据类型有哪些？
39. String 底层 SDS 是什么？
40. Hash 扩容过程是什么？
41. Redis 过期删除策略是什么？
42. Redis 内存淘汰策略有哪些？
43. RDB 和 AOF 区别是什么？
44. 缓存穿透怎么解决？
45. 缓存击穿怎么解决？
46. 缓存雪崩怎么解决？
47. 热 key 怎么解决？
48. Redis Lua 为什么原子？
49. 分布式锁怎么做？Redlock 有什么争议？
50. Redis 和 MySQL 如何保证最终一致？

### MySQL 51-65

51. MySQL 一条 SQL 执行过程是什么？
52. InnoDB 和 MyISAM 区别是什么？
53. B 树和 B+ 树区别是什么？
54. 聚簇索引和非聚簇索引区别是什么？
55. 联合索引最左匹配原则是什么？
56. 哪些情况会索引失效？
57. 事务 ACID 是什么？
58. 四种隔离级别是什么？
59. MVCC 原理是什么？
60. 当前读和快照读区别是什么？
61. 行锁、间隙锁、临键锁区别是什么？
62. redo log、undo log、binlog 区别是什么？
63. 如何排查慢 SQL？
64. 高并发下如何扣库存？
65. 分库分表后如何查询和扩容？

### MQ 66-78

66. 为什么使用消息队列？
67. MQ 如何削峰？
68. RabbitMQ producer/consumer/exchange/queue 是什么？
69. ack 和 nack 区别是什么？
70. auto ack 和 manual ack 区别是什么？
71. prefetch 是什么？
72. 如何保证消息不丢？
73. 如何处理重复消费？
74. 消息积压怎么排查？
75. 死信队列是什么？
76. 延迟队列是什么？
77. 顺序消息怎么保证？
78. RabbitMQ 和 Kafka 区别是什么？

### 高并发与分布式 79-90

79. 秒杀系统如何设计？
80. 如何防止超卖？
81. 如何防止重复下单？
82. 限流算法有哪些？
83. 令牌桶和漏桶区别是什么？
84. 如何设计分布式限流？
85. 如何做接口幂等？
86. 什么是最终一致性？
87. 如何做补偿任务？
88. 服务降级和熔断是什么？
89. QPS、RT、TP99 怎么看？
90. 压测发现瓶颈后如何定位？

### 网络/OS/工程 91-100

91. HTTP 请求过程是什么？
92. HTTP 和 HTTPS 区别是什么？
93. TCP 三次握手和四次挥手是什么？
94. TIME_WAIT 为什么存在？
95. TCP 粘包是什么？
96. 进程、线程、协程区别是什么？
97. Linux 如何查看端口、进程、CPU、内存？
98. 服务收到 Ctrl+C 后发生什么？
99. 如何做日志和 request_id 链路追踪？
100. Prometheus 指标类型有哪些？

## 后续核验任务

- 逐篇阅读全文，把“待全文核验”改成“已核验”。
- 为每个 Top100 问题标注至少一个来源序号。
- 把 Top100 回填到 `08_MOCK_INTERVIEW_BANK.md`，形成模拟面试轮次。
