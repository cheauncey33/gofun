# 赴场面试话术对照

本文把「仓库里已有的实操」和「评论 / 抢票本地缓存新补的样板」对照写清，方便直接讲。

## 1. 评论讨论区（阶段 1）

产品定位：**登录即可参与的活动讨论区**，不要求已购票。防刷靠限流，不靠购票门槛。

### API

| 方法 | 路径 | 鉴权 |
|------|------|------|
| GET | `/api/v1/events/:id/comments` | 可选（有 token 时返回 `is_owner`） |
| POST | `/api/v1/events/:id/comments` | 登录 |
| DELETE | `/api/v1/comments/:id` | 登录（仅本人） |
| POST | `/api/v1/comments/:id/like` | 登录 |

### Redis Key

| Key | 作用 |
|-----|------|
| `fuchang:comment:list:{eventId}` | 第一页列表 Cache-Aside（JSON，最多约 50 条） |
| `fuchang:comment:rl:{userId}` | 发评用户维限流（1 分钟最多 10 条） |
| `fuchang:comment:like:{eventId}` | 点赞计数 ZSET（member=commentId, score=赞数） |
| `fuchang:comment:liked:{userId}:{commentId}` | 点赞幂等（SETNX） |

### 面试标准答法

**为什么按活动拆 key，而不是一个全局 comments？**  
读多写少的列表缓存，按资源拆分；单 key 只存该活动近期评论投影，控制体积，避免 bigkey。监控可看 `MEMORY USAGE`。

**Cache-Aside 怎么保证和 DB 大致一致？**  
读：先 Redis，未命中查 MySQL 再回填。写（发评/删评/点赞）：先落库或更新热数据，再 `DEL` 对应 list key。接受短暂不一致，用删缓存而不是双写更新。

**空列表为什么也缓存？**  
防缓存穿透：无人评论或刷不存在活动时，短 TTL（约 30s）挡住打穿 DB。

**点赞为什么用 ZSET + 定时 flush？**  
热路径 `ZINCRBY`，排行/计数读 Redis；后台定期把 score 刷回 `like_count`。用户是否点过赞用 SETNX 幂等。

**和全局写接口限流的关系？**  
接口层仍有 write limiter；评论再加用户维 `INCR+EXPIRE`，专门防讨论区刷屏。

---

## 2. 抢票库存本地缓存（阶段 3）+ 直抢语义

秒杀产品语义：**到点直接买**，不做候场排队，也不再要求先领 rush token。

防刷靠：登录 + 写接口限流 + `X-Idempotency-Key` + Lua 内个人限购计数。

目标（本地缓存）：开售瞬间大量用户刷「剩余票额」时，减轻对 Redis `fuchang:rush:stock:{campaignId}` 的 **hotkey 读压力**。

### 分层

```text
用户：填写信息 → POST /rush-sales/:id/execute（一次）
扣减：Redis Lua（双库存 + 限购）
列表读余量：进程内 local cache (TTL ≈ 300ms)
           → miss 时 singleflight 合并
           → Redis GET
```

| 层级 | Key / 位置 | 职责 |
|------|------------|------|
| L1 本地 | `local:rush:stock:{campaignId}`（go-cache） | 极短 TTL 读缓存 |
| L2 Redis | `fuchang:rush:stock:{campaignId}` | 权威预扣库存 |
| 扣减 | `executeRushSaleScript` Lua | 原子扣减，不走本地 |

### 面试标准答法

**为什么不做候场排队？**  
候场是大盘票务的削峰手段；本项目定位秒杀直抢：流量打到库存原子扣减上，用 Redis Lua + MQ 异步建单扛并发。排队会把「秒杀」讲成「放号」，产品叙事不一致。

**以前为什么有 token？现在为什么去掉？**  
旧 token 是防脚本/重放的一次性凭证，但被做成了显式两步，用户像在「先抢令牌」。直抢后闸门下沉到限流、幂等和 Lua 限购，对外就是一次购买。

**为什么本地缓存不能用来扣库存？**  
多实例各自一份内存，扣减走本地会超卖。正确性仍交给 Redis Lua；本地只削峰读请求。

**TTL 为什么这么短（约 300ms）？**  
余量展示允许短暂不准；TTL 越短，跨实例滞后窗口越小。比商品列表那种 30s 本地缓存激进得多。

**和 singleflight 的关系？**  
同一 key 并发 miss 时只打一次 Redis，避免本地集体失效瞬间变成击穿。

**多实例怎么办？**  
不追求强一致广播。本节点扣减后立即对齐；其它节点最多落后一个 TTL，再从 Redis 拉新。展示场景可接受。

**hotkey 还能怎么扩展（口头）？**  
读写分离副本、本地缓存、限流。库存计数一般不打散 key（要原子）。本项目落地的是「二级短缓存 + Lua 扣减」。

---

## 3. 本仓库已有、可直接讲的场景

### Redis / 缓存

| 场景 | 落点 | 怎么答 |
|------|------|--------|
| bigkey 拆分 | 票档库存、抢票、评论 list 均按资源拆 key | 拆分 + 监控，大 value 用分页/投影 |
| hotkey | 抢票 stock + 本地短 TTL | 读走 L1，写走 Redis Lua |
| Lua 原子扣减 | 普通票 / 抢票预扣 | 多命令当一事务，减少竞态 |
| 分布式锁 | 下单用户锁 `lock:*` | SET NX EX + value 校验再删 |
| 库存对账 | 补偿任务 | Redis > MySQL 时把 Redis 拉回，防超卖窗口 |

### MQ / 异步

| 场景 | 落点 | 怎么答 |
|------|------|--------|
| 削峰 | 下单进队列，接口快速返回 | 前端「处理中」与最终一致 |
| 重试 / 死信 | retry + DLQ | 可重试 vs 不可重试；超限人工看 |
| 延迟关单 | `fuchang.order.delay` TTL→DLX→`fuchang.order.timeout` | 进 `pending_payment` 后投递；消费端条件取消；DB 扫描兜底 |
| 至少一次 → 幂等 | 订单 ID / 幂等键 | 消费端按业务键去重 |

### 一致性 / 交易

| 场景 | 落点 | 怎么答 |
|------|------|--------|
| 超卖防护 | Redis 预扣 + MySQL 落库 | 预扣失败直接拒绝；落库失败回滚预扣 |
| 支付与超时竞态 | `cancelPendingPaymentOnly` + 行锁 | 仅 `pending_payment` 可关；已支付跳过，不误退 |
| 幂等键 | 客户端 UUID + SETNX | 防重复提交 |

### 工程

JWT 双令牌、全局限流 + IP 限流、Snowflake、Prometheus、优雅退出、票务 QR 密钥轮换。

### 别硬吹

布隆过滤器、分库分表、Seata：当前用对账 + 幂等 + 状态机即可，面试如实说边界。

---

## 4. 阶段 2：活动目录 ES 全文检索

ES = **Elasticsearch**（搜索引擎组件），不是一种算法。倒排索引 + 分词是其内部机制。

### 配置

```yaml
elasticsearch:
  enabled: true|false
  addresses: ["http://127.0.0.1:9200"]
  index: fuchang_events
  search_engine: auto   # mysql | elasticsearch | auto
```

| search_engine | 行为 |
|---------------|------|
| `mysql` | 始终 MySQL `LIKE` |
| `elasticsearch` / `auto` | 有 `keyword` 时优先 ES；失败日志后降级 LIKE |
| `enabled: false` | 不连 ES，等价 mysql |

### 链路

```text
GET /events?keyword=夏夜
→ Prefer ES?
   yes → multi_match 倒排检索 → 得到 event IDs
        → MySQL 按 ID 水合（Organizer/Sessions/票档）
        → ES 挂了 / 超时 → MySQL LIKE 降级
   no  → MySQL LIKE（title/subtitle/category/venue）
发布/取消/下架 → 同步 Index / Delete ES 文档
启动 → ReindexPublishedEvents（best-effort）
```

索引字段：`title/subtitle/search_text/venue_text`（ngram，无 IK 插件也能搜中文子串），`category/city` keyword 过滤。

### 面试标准答法

**ES 是数据库吗？**  
偏搜索引擎。MySQL 仍是业务权威源；ES 存检索投影（最终一致）。

**为什么还要 MySQL 水合？**  
ES 只存搜得到的字段和 ID；详情关联仍从 MySQL 读，避免双写复杂对象。

**倒排是什么？**  
词 → 文档 ID 列表。`LIKE %夏夜%` 难走普通 B+ 树；倒排按词查文档更适合全文。

**可切换 / 降级？**  
`search_engine=mysql` 强制旧路径；`auto` 优先 ES，异常回退 LIKE，保证可用性。

---

## 5. 手动验证清单

### 讨论区

1. 打开活动详情 → 讨论区可见；未登录可浏览。
2. 登录后发评 → 列表首条出现；刷新仍在（DB）；再次打开命中缓存。
3. 1 分钟内连发超过 10 条 → 返回限流错误。
4. 点赞 → `like_count` 增加；再点 → `already_liked`。
5. 本人可见「删除」；删除后列表与缓存更新。

### 抢票本地缓存

1. `GET /rush-sales` 返回的 `remaining_quota` 在 Redis 有 stock key 时应跟 Redis 余量走。
2. 抢票成功后同节点再次列表，余量应立刻下降（Lua 返回值回写 local）。
3. 扣减失败（售罄）时本地记 0；回滚预扣后本地被 Delete，下次从 Redis 再读。

### 抢票并发证据（秋招优先）

```powershell
$env:BASE_URL='http://127.0.0.1:18080/api/v1'
node tests/integration/rush_concurrency.mjs
```

脚本自建 campaign，输出 JSON 的 `cases[]`。面试可直接说：「同幂等键并发只一单；限购并发卡死上限；N 人抢 M 张恰好 M 成功。」

### ES 全文检索

1. `elasticsearch.enabled=true` 时，`GET /events?keyword=夏夜` 应能命中标题/副标题相关活动。
2. 停掉 ES 或设 `search_engine=mysql` → 同一 keyword 仍可用（降级 LIKE）。
3. 发布新活动后可被搜到；取消/下架后不再出现在 keyword 结果中。
