按照问题定义、问题排查、问题解决、实际案例分析的思路来

1、首先是大key问题的定义单个key的value过于大，常见包括两类：一种是string内容过大；一种是集合元素过多：比如list/hash/set/zset。Redis高性能的主要原因是在内存中单线程地处理请求，上下文开销小。但是面对大key时，因为value过大，会容易拉长执行时间，阻塞其他请求，放大延迟。大key还会带来网络传输、内存分配、持久化、复制和删除等额外开销。

大key可能带来的问题：
1、阻塞主线程：读写删大key都可能耗时，Redis处理其他请求延迟就会变高。

2、网络传输变大：大key往往超过10mb，网络IO开销大
3、内存压力和OOM，影响淘汰策略：如果淘汰策略（比如LRU/LFU这种按照key维度淘汰，不过一般redis的淘汰策略都是按key的，LFU是指）不合适，可能会超过maxmemory导致OOM。或者因为大key太大，有比较热，会挤压其他热点key的空间，连续淘汰很多小热点key。
4、影响持久化和主从复制：大key肯定会导致复制延迟。删除或者修改大key，也会把相应的命令同步到从库，从库就要处理释放内存的事。

2、怎么发现redis的大key问题？
先定位：redis-cli --bigkeys、--memkeys、--keystatus，配合SCAN，再结合showlog、latency monitor看哪些命令拖慢实例。

3、解决思路是什么？
核心不是把大key删掉，而是把数据模型拆小：
1、按照业务拆分key，避免一个key无限长（核心）  也即优化数据结构。比如弹幕本来按照房间号、日期分——danmu:room6373:20260523可以再按照小时细分为——danmu:room6373:20260523:15_00
2、有些数据只保存必要字段，比如文章的详情、正文或者历史数据这一类都可以外置到db中。
3、对于已经存在的大key问题，删除重构时使用unlink（后台线程异步执行）而不是DEL（redis指令，阻塞redis主线程）**此处需要纠正：UNLINK和DEL都是redis的命令，不过前者是异步删除，把 key 从 keyspace 里摘掉，业务上查不到它，真正释放内存会交给后台线程异步完成，但是DEL是同步删除，本身就会导致阻塞**。

4、无法避免的大key问题可以配合ttl、定期裁剪、分页读取。





redis说是串行，其实是主线程严格串行，也就是**命令执行是由主线程串行处理的**。还是有后台线程，比如：UNLINK会调用后台线程。
Redis的常见数据结构：string、hash、list、set、zset。

常见的指令：


对于大key的什么操作会导致阻塞？比如DEL big_keys/HGETALL big_keys（Hash get 所有内层key）/LRANGE 0 -1（List的全部遍历）
常见的淘汰策略：noeviction/allkeys-lru/volatile-lru/allkeys-lfu/voatile-ttl。其中LFU 是 **Least Frequently Used**，最不经常使用。看的是“访问频率高不高”。