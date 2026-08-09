# Gofun 面试材料入口

这份目录只保留当前票务实现相关的面试资料。先读总控，再按问题回到代码和阶段文档。

## 使用顺序

1. 读取 00_CONTEXT_LOCK.md，确认项目边界、链路和未实现项。
2. 读取 项目按照链路、接口梳理/2、关于下单和秒杀.md，准备购票/抢票主链路回答。
3. 需要证据时查看 docs/CONSISTENCY.md、docs/FUCHANG_PAYMENT_SANDBOX.md、
   docs/FUCHANG_ADMISSION_TICKET.md 和 tests/load/results/ 下的历史报告。
4. 通用 Redis、MQ、缓存知识只作为通用知识，不要把没有当前代码依据的内容说成已实现。

## 维护规则

- 以 backend/main.go、backend/service/ 和 frontend/src/ 的当前代码为准。
- 每个结论注明“已实现”“当前限制”或“后续方案”。
- 文档与代码冲突时修改文档，不用历史材料覆盖当前实现。
