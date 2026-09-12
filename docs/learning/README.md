# MeshOps 学习入口

**从 [Z00—Z13 课程目录](from-zero/lessons/README.md) 开始。** 按需求 → 协议 → 单服务 → 持久状态 → 可靠任务逐步搭建，同一个学习工程持续演进。

| 需要什么 | 入口 |
| --- | --- |
| 每课讲解、完整代码与顺序 | [课程目录](from-zero/lessons/README.md) |
| 阶段内精确操作 | [28 个分步操作](from-zero/lessons/steps/README.md) |
| 启动和多终端环境 | [阶段运行手册](from-zero/CHECKPOINTS.md) |
| 日志与纠错 | [排错实操](from-zero/lessons/debugging.md) |
| 查看最终项目 | [完整参考工程](../../README.md) |
| 看清目录用途 | [材料导航](from-zero/README.md) |

## 学习方式

学习工程约定为 `D:\job\golang\projects\meshops-course-lab`，由课程步骤创建。教材保存在本仓库 `docs/learning/`，不要把教材目录当作自己的业务工程。

每步先解释为什么需要某个功能、字段和文件，再给完整代码、运行命令、预期输出和错误分析。你可以直接复制后理解，也可以先尝试再对照，必需代码不会留给你自行搜索补齐。复制时使用“完整文件答案”，不要把解释片段或 Markdown 围栏当源码。

## 当前状态

参考工程已实现状态、任务、搜索、HTTP/SSE 网关、Vue 控制台、模拟器与 Docker 演示。Z00 讨论需求，Z01—Z13 共 13 个代码阶段，Z04—Z13 共 28 个分步操作。连续复制、构建、真实依赖与浏览器验证各有适用范围，见 [工作记录](from-zero/BUILD-LEDGER.md)；不能把历史一次通过视为所有后续修改都已验收。

用户学习进度单独记录在 [学习记录](../implementation/learning-progress.md)，不能用 AI 写完代码代替用户完成学习。

## 辅助资料

- [教学路线](from-zero-roadmap.md)：每阶段为什么这样安排。
- [代码交付标准](reference-code-standard.md)：完整、可复制、可验证的要求。
- [验收对应](acceptance-map.md)：阶段与验收边界。
- [基础补课](resources-and-remediation.md)：遇到问题时查阅。
- [维护入口](from-zero/maintenance/README.md)：供教师与 AI 使用。

已删除被新版替代的旧 50 课、旧独立示例与旧课程说明，不再维护第二套授课顺序。
