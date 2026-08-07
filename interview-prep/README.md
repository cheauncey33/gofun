# 赴场 · 面试材料入口

> 仓库已从零食电商迁移为 **赴场票务**。八股与场景题原文可保留；项目链路以 `00_CONTEXT_LOCK.md` 为准。

## 使用顺序

1. 先读 `00_CONTEXT_LOCK.md`，确认回答不偏离 **当前票务代码**。
2. 再读 `docs/FUCHANG_INTERVIEW.md`（评论区 / 抢票缓存 / ES / 压测证据）。
3. 链路梳理：`项目按照链路、接口梳理/`（其中「2、关于下单和秒杀」已改写为购票/抢票）。
4. 八股背诵：`感觉一定会问的题目：.md`（Redis/MQ/缓存等通用部分仍可用；涉及 product/seckill/用户锁的段落以 00 为准）。
5. 白盒与 schema：`01_PROJECT_WHITEBOX.md`、`02_DATABASE_SCHEMA.md` — **部分仍为旧零食描述，阅读时对照 00 修正**。
6. 模拟题：`08_MOCK_INTERVIEW_BANK.md`、`06_SCENARIO_QA.md`。

## 维护规则

- 新增内容先对照 `00_CONTEXT_LOCK.md` 和 `backend/main.go`。
- 代码与文档冲突时 **以代码为准**。
- 生产扩展方案须标注「当前未实现」。
