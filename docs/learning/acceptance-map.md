# 课程与验收对应

教学顺序见 [课程目录](from-zero/lessons/README.md)，业务定义见 [A01—A28](../implementation/acceptance.md)。本页只做导航，不建立另一套标准。

| 范围 | 主要阶段 | 要验证什么 |
| --- | --- | --- |
| A01—A04 | Z04，后续建库与完整接线补验 | 配置、身份/资源隔离、迁移、来源适配 |
| A05—A08 | Z05—Z06 | ACK、重连、版本、墓碑、故障与重放 |
| A09—A13 | Z06 | 六类实体、订阅边界、慢消费者、持久队列 |
| A14 | Z08 | 真实历史存储、采样去重、分页与预算 |
| A15—A23 | Z07 | 任务事务、Outbox、分发、执行、取消、超时与死信 |
| A24 | Z08 | 完整操作端及演示；最小客户端从 Z02 就开始 |
| A25—A28 | Z09 | 重建完整性、恢复边界、指标、性能与复现 |

跨阶段要求必须补齐全部子项。基础阶段构建成功不表示最终可靠性验收完成；协议步骤没有业务测试，SQL 解析也不等于真实 MySQL 验证。

- [业务逐项证据](../../verification/2026-09-10/acceptance.md)
- [业务验收汇总](../../verification/2026-09-10/summary.md)
- [整阶段运行](from-zero/verification/2026-09-10-checkpoints.md)
- [20 步构建与指定测试](from-zero/verification/2026-09-10-remaining-steps.md)
- [完整交付标准](reference-code-standard.md)

新增范围分别验收：Z10 搜索与独立学习目录恢复见 [整体复核](from-zero/verification/2026-09-12-integrated-review.md)；Z11—Z13 前后端见 [全栈发布](../verification/2026-09-12-fullstack/release.md)；六类运动与混合压测见 [场景验证](../verification/2026-09-12-motion-mixed/README.md)。不能直接套用原 A01—A28 的通过结果。
