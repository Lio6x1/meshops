# 设计与实施记录的状态

这些按日期保存的文档解释当时的设计取舍和实施过程，不是当前未完成任务清单。实际功能见 [完整工程](../../README.md)，维护与教程见 [当前入口](../implementation/README.md)。

- 8 月需求、架构、选型和 9 月 5 日实施方案：保留业务背景、取舍和契约追溯；旧骨架路径不再是当前操作入口。
- 9 月 10—12 日课程、迁移、搜索、复核与全栈方案：已有后续交付，按各自日期阅读；中途失败与未勾选项不自动代表现存缺陷，完成范围查具体验收记录。
- [百万实体压测草案](plans/2026-09-13-scale-benchmark.md)：保留最初讨论和当时暂停的上下文。用户后续已恢复执行，当前范围由[账号与容量设计](specs/2026-09-13-accounts-scale-design.md)及[实施计划](plans/2026-09-13-accounts-scale-implementation.md)约定，不能把旧暂停状态当成当前指令。
- 当前个人账号已实现并完成真实数据库、HTTP/RPC/SSE 撤销与个人任务审计等回归；使用[账号手册](../operations/accounts.md)初始化和恢复。最新 Windows 普通测试为 [168 PASS（unit-release）](../../verification/2026-09-13-accounts-scale/unit-release.summary.json)，先前 Linux 普通 race 为 162 PASS；两次普通运行各有 22 项 SKIP（20 项外部依赖门控、2 项 edge/state 崩溃子进程辅助入口），完整真实依赖 race 待最终 CI 验收。独立规模工具已实现，按[规模压测手册](../operations/scale-benchmark.md)控制实体基数、事件速率、资源和硬时限；百万容量与教程最终复制验收仍待完成，没有百万容量通过结论。

可复现命令统一使用 [启动手册](../run-fullstack.md) 和 [课程维护说明](../learning/from-zero/maintenance/README.md)。已经被替代且没有决策或证据价值的成套示例不在这里另存副本。
