# 赴场面试话术对照（阶段 1）

本文把「仓库里已有的实操」和「评论讨论区新补的缓存样板」分成两块，方便对照回答。

## 1. 评论讨论区（本阶段新增）

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

## 2. 本仓库已有、可直接讲的场景

### Redis / 缓存

| 场景 | 落点 | 怎么答 |
|------|------|--------|
| bigkey 拆分 | 票档库存、抢票、评论 list 均按资源拆 key | 拆分 + 监控，大 value 用分页/投影 |
| Lua 原子扣减 | 普通票 / 抢票预扣 | 多命令当一事务，减少竞态 |
| 分布式锁 | 下单用户锁 `lock:*` | SET NX EX + value 校验再删 |
| 库存对账 | 补偿任务 | Redis > MySQL 时把 Redis 拉回，防超卖窗口 |

### MQ / 异步

| 场景 | 落点 | 怎么答 |
|------|------|--------|
| 削峰 | 下单进队列，接口快速返回 | 前端「处理中」与最终一致 |
| 重试 / 死信 | retry + DLQ | 可重试 vs 不可重试；超限人工看 |
| 至少一次 → 幂等 | 订单 ID / 幂等键 | 消费端按业务键去重 |
| 延迟关单 | TTL + DLX | 与支付竞态用条件更新 |

### 一致性 / 交易

| 场景 | 落点 | 怎么答 |
|------|------|--------|
| 超卖防护 | Redis 预扣 + MySQL 落库 | 预扣失败直接拒绝；落库失败回滚预扣 |
| 支付与超时竞态 | 条件更新状态机 | `RowsAffected==0` 拒绝双花 |
| 幂等键 | 客户端 UUID + SETNX | 防重复提交 |

### 工程

JWT 双令牌、全局限流 + IP 限流、Snowflake、Prometheus、优雅退出、票务 QR 密钥轮换。

### 别硬吹

布隆过滤器、分库分表、Seata：当前用对账 + 幂等 + 状态机即可，面试如实说边界。

---

## 3. 阶段 2 / 3（未做，口头可延伸）

- **阶段 2**：活动目录 ES 全文，与 MySQL LIKE 可切换。
- **阶段 3**：抢票库存进程内短 TTL 本地缓存，缓解 hotkey；扣减仍走 Redis Lua。

---

## 4. 手动验证清单

1. 打开活动详情 → 讨论区可见；未登录可浏览。
2. 登录后发评 → 列表首条出现；刷新仍在（DB）；再次打开命中缓存。
3. 1 分钟内连发超过 10 条 → 返回限流错误。
4. 点赞 → `like_count` 增加；再点 → `already_liked`。
5. 本人可见「删除」；删除后列表与缓存更新。
