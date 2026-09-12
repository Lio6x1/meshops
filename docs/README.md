# MeshOps 文件导航

当前只维护一套完整工程和一套从零教程。根目录代码是完整成品；教程阶段答案是学习过程中的中间版本，不是多套部署。

| 目的 | 从这里进入 |
| --- | --- |
| 先运行成品 | [前后端启动手册](run-fullstack.md) |
| 从创建目录开始学习 | [Z00—Z13 课程](learning/from-zero/lessons/README.md) |
| 了解功能与链路 | [根工程说明](../README.md)、[实体范围](entity-catalog.md) |
| 查看模拟器与地图 | [场景说明](simulation-map.md) |
| 准备面试 | [项目介绍与追问](interview/interview-prep.md)、[速查](interview/cheatsheet.md) |
| 查技术规则与错误 | [契约入口](implementation/README.md)、[排错手册](troubleshooting/README.md) |
| 判断已证明到什么程度 | [工程验证边界](production-readiness-checklist.md) |
| 修改代码与同步教材 | [课程维护入口](learning/from-zero/maintenance/README.md) |

## 目录用途与保留理由

| 位置 | 用途 |
| --- | --- |
| 根 `cmd/internal/proto/gen/configs/migrations/scripts/web/deploy` | 当前应用、协议、配置、前端和部署 |
| `learning/from-zero/lessons` | 学生讲义、完整文件答案与分步操作 |
| `starter/state-stages/business-stages/web-stages` | Z01—Z12 阶段源码；Z13 使用根工程；由发布器和指纹校验维护 |
| `implementation`、`adr` | 当前契约与技术决策依据；ADR 明确区分最初方案与后续落地 |
| `superpowers` | 按日期保存的设计和实施过程；[状态说明](superpowers/README.md) 区分历史与暂停草案，不能作自动执行指令 |
| `ui` | 当前真实前端的功能与交互说明，旧离线原型已删除 |
| 根 `verification`、`docs/review`、`docs/verification` | 不同日期的业务验收、问题复核与新增功能证据；不改写旧测试结果 |
| `learning/from-zero/verification` | 教程复制、阶段演进、排错等教学验证 |
| `.local`、`data`、`results` | 本机凭证、运行状态、持久队列及结果；不能按“旧代码”删除 |
| `.cache`、`bin`、`web/node_modules`、`web/dist` | 本机工具、构建缓存与可重建产物；不是提交到仓库的业务源码 |

自己的代码写在同级 `meshops-course-lab`，按 Z01 创建，不把整个参考仓库复制进去。历史材料仅用于追溯，当前启动、课程与功能判断使用上面的入口。
