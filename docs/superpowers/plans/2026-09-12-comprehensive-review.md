# MeshOps Comprehensive Review Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 完成用户确认的全面复核与修复、必要代码注释、本地问题记录和前端 UI 设计方案。

**Architecture:** 保留现有服务与教学阶段划分。按模块并行核对与修复，主执行者统一处理接口和课程发布，所有依赖停止/重建演练顺序执行。UI 方案依据最终确认的接口设计。

**Tech Stack:** Go 1.25.10、go-zero、gRPC/Protobuf、MySQL 8、Redis 7、Kafka、bbolt、Canal、Elasticsearch、PowerShell、Python 3 标准库、HTML/CSS/JavaScript 设计原型。

**Spec:** [已确认范围](../specs/2026-09-12-comprehensive-review-design.md)

## Global Constraints

- 工作基线 `641bf62`；工作目录为 `.worktrees/comprehensive-review-20260912`，修复最终整合回主项目。
- 保留六类实体、四类 inspect 执行方、单权威来源与学习项目范围。
- 不删除已有数据库卷、用户学习代码、真实凭证或历史证据；实验使用隔离库、主题、前缀和目录。
- 每个实际行为修复须有针对性回归，依赖测试不得把缺少环境当作验收成功。
- 根源码与受影响阶段、生成文件答案、分步清单同时更新；生成的 Proto 文件只能使用现有生成器。
- 修复前先审查条目是否仍成立；数据库性能结论需实际执行计划或测量。

## A. 建立基线与问题清单

- [x] 确认 Git 基线、干净工作区和独立工作目录。
- [x] 分配只读审查：任务、状态/Kafka、边缘；主执行者审查搜索、平台、装配、CI、脚本和材料。
- [x] 运行当前根普通测试，保存基线结果。
- [x] 在 assessment.md 逐条列出 F1—F5、C1—C9、T1—T6、D1—D6、H1—H2 和结构性边界。
- [x] 核对搜索装配、引导完成标记、严格消费及维修入口的实际代码，不根据旧行号修改。
- [x] 审查全部根手写模块，并将新增发现纳入同一清单。

## B. 业务与并发修复

**Files:** `internal/tasks/`、`internal/bus/`、`internal/state/`、`internal/edge/`、`internal/search/`、`internal/platform/`、必要的 `internal/app/` 和 `proto/`。

**Interfaces:** 跨模块接口由主执行者确认；当前任务回报使用 TaskService → Dispatcher 验证投递身份，状态投影实现 Consumer，Search 使用严格 Kafka 消费与引导标记。

- [x] 每个确认缺陷先建立能区别当前错误与期望行为的回归，用实际运行记录证明失败。
- [x] 实现最小修复，保留租户隔离、来源版本、任务状态和执行幂等约束。
- [x] 检查错误能否被观察、是否会停止服务或阻塞分区、恢复入口是否可执行。
- [x] 为历史清理执行实际 EXPLAIN，按测量结果决定是否拆分清理查询。
- [x] 检查订阅扇出与 inbox 扫描，修复可避免的放大；持久幂等记录的回收须有重放安全边界。
- [x] 运行相关普通、真实依赖和并发回归；独立复核实现与测试是否满足原行为约束。

## C. 测试入口与 CI

**Files:** `.github/workflows/ci.yml`、`scripts/test.ps1`、`internal/testsupport/`、相关真实依赖测试、`testdata/proto/README.md`。

- [x] 核对所有环境门控、子进程测试和被跳过的路径；显式集成验收必须证明关键用例执行。
- [x] 本地集成入口包含 ES 必需配置；统一 Staticcheck 版本和协议工具获取说明。
- [ ] 在启动依赖后对 state/bus/edge/tasks/search 的真实路径执行 race，记录所需用例和异常退出。
- [x] 补充审查确认必要的缺失测试，覆盖实际行为而非逐行复述代码。
- [x] 用完整配置运行测试入口，并保存 JSON 结果；不把普通测试中的 SKIP 计为通过。

## D. 必要注释、课程同步与文档一致性

**Files:** 根手写代码及相应 `docs/learning/from-zero/{starter,state-stages,business-stages}`，维护脚本和讲义，README、工程验证清单。

- [x] 补充各模块职责、核心入口、不变量、并发/事务/资源生命周期、参数单位和异常恢复注释。
- [x] 为复杂回归解释触发条件及其证明的性质；不手改生成代码。
- [x] 逐项修正文档中当前状态、历史记录、阶段差异和能力边界的歧义。
- [x] 扫描全部本地文档链接；区分代码示例内路径和真实 Markdown 链接。
- [x] 同步受影响阶段，更新文件指纹，运行以下维护发布与检查：

```powershell
./docs/learning/from-zero/publish-search-checkpoint.ps1
./docs/learning/from-zero/publish-file-guides.ps1
./docs/learning/from-zero/publish-substeps.ps1
python ./docs/learning/from-zero/verify-git-checkpoints.py
./docs/learning/from-zero/verify-file-guides.ps1 -Build
./docs/learning/from-zero/test-checkpoints.ps1 -AllStages
```

## E. 本地问题知识库

**Files:** `docs/troubleshooting/README.md`、按问题拆分的 Markdown 条目、`docs/review/2026-09-12/assessment.md`。

- [x] 收录此前 Git 换行、Staticcheck、人工重试死锁，以及本轮已确认的问题。
- [x] 每条写明现象、影响、复现、根因、解决方法、验证命令与结果、适用边界。
- [x] 对连接、鉴权、业务拒绝、数据一致性、恢复与性能建立可导航的排查入口。
- [x] 日志与记录不包含真实令牌、密码或用户队列数据。

## F. UI 设计方案

**Files:** `docs/ui/README.md`、`docs/ui/design.md`、`docs/ui/prototype/index.html` 和本地静态资源。

- [x] 根据实际协议确定实体概览/详情、任务列表/详情/创建、任务搜索和运行状态入口。
- [x] 明确数据新鲜度、未知字段、加载、空结果、断连、错误、只读与权限不足等状态。
- [x] 映射已有 RPC 与必要的浏览器接入方案，明确需新增接口的部分。
- [x] 制作可浏览交互线框，使用明确标注的模拟数据，不触发真实业务操作。
- [x] 完成布局/交互的静态与 Node VM 检查，交付设计原型；浏览器访问被工具策略拒绝，实际视觉、键盘焦点和截图未验收，已在 UI 文档明确。

## G. 最终验收与整合

- [x] 先顺序完成相关真实依赖回归及 fault/recovery，保留启动前的参考服务状态。
- [x] 在全新学习目录按教程完成初始化、六类上报、查询/订阅、四类任务、取消与重试、历史查询和搜索同步。
- [x] 验证搜索初始化失败/索引丢失的恢复、新任务继续同步，以及审查新增的恢复场景。
- [x] 完成必要的有边界性能实验与 EXPLAIN，注明测试条件及不能外推的结论。
- [ ] 验证 Proto、静态检查、普通测试、真实依赖 race、完整答案复制、分步构建和 Git 检出指纹。
- [ ] 将变更整合回主项目，确认本地记录和 UI 方案入口可打开，检查 Git 文件范围和远端 CI。
- [ ] 输出四项交付与证据，明确剩余工程边界，不以测试通过保证不存在其他缺陷。
