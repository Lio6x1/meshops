# 课程维护入口（教师与 AI）

学生请使用 [课程目录](../lessons/README.md)。这里说明材料依赖关系，不是必修课程。

## 哪一份是依据

| 内容 | 依据与规则 |
| --- | --- |
| 最终业务实现 | [根目录完整工程](../../../../README.md)，对应最终 Z13；module 为 example.com/meshops-course，旧框架已删除 |
| 教学顺序 | [新课程目录](../lessons/README.md) 与 [路线取舍](../../from-zero-roadmap.md) |
| 阶段文件及指纹 | [checkpoint-index.json](../checkpoint-index.json)，对应 starter、state-stages、business-stages、web-stages；Z10 已固定到 business-stages/z10，Z11/Z12 是 web-stages 快照，Z13 从根工程发布 |
| 小步骤文件及测试 | [substep-index.json](../substep-index.json)，由发布脚本维护 |
| 交付要求 | [完整代码标准](../../reference-code-standard.md) |
| 当前状态与剩余工作 | [工作记录](../BUILD-LEDGER.md)；已注明历史的段落不作当前待办 |
| 成品实际启动和双模式切换 | [完整前后端启动手册](../../../run-fullstack.md)，学生代码位于兄弟 meshops-course-lab，参考教材不整体复制过去 |
| 账号生命周期与恢复 | [个人账号手册](../../../operations/accounts.md)，区分 Local/Docker 数据库及 Setup/Reset |
| 大规模试验与失败判据 | [规模压测手册](../../../operations/scale-benchmark.md)，独立实体基数、事件速率、资源、时限和完整预热 |

修改最终代码前先判断影响哪些教学阶段，再同步对应阶段、生成讲义并验证。不要直接编辑生成的文件答案或 Proto 生成代码。阶段代码的重复是教学快照；是否存在业务内部不必要的重复，需要代码审查，不能通过删除快照解决。

`substeps/go.mod` 只隔离不完整的教学过渡片段，防止根工程 `go test ./...` 将它们当业务包测试。片段必须按清单复制到完整学习工程后验证；它不是另一套应用，不单独运行。

## 脚本分类

这些脚本继续放在 `from-zero/`，因为使用 `$PSScriptRoot` 定位相邻教材。仅为了目录整齐移动它们，会改变运行语义。

| 工具 | 用途 |
| --- | --- |
| [apply-checkpoint.ps1](../apply-checkpoint.ps1) | 学生可选的受管理整阶段复制，不与手工路线混用 |
| [materialize-checkpoints.ps1](../materialize-checkpoints.ps1) | 维护阶段快照；会写文件，不能当普通检查运行 |
| [publish-lesson-code.ps1](../publish-lesson-code.ps1) | 同步 Z01—Z03 正文代码；`-Check` 只检查 |
| [publish-file-guides.ps1](../publish-file-guides.ps1) | 生成阶段完整文件答案；`-Check` 只检查 |
| [publish-search-checkpoint.ps1](../publish-search-checkpoint.ps1) | 检查冻结 Z10 的来源和指纹；不再把根工程写成 Z10 |
| [publish-fullstack.py](../publish-fullstack.py) | 从固定 Z10 累积发布 Z11 账号与网关、Z12 Vue 和根工程 Z13；排除凭证、缓存、node_modules、dist，随后再生成文件答案/分步清单 |
| [publish-substeps.ps1](../publish-substeps.ps1) | 生成分步清单、说明和过渡文件；会写文件 |
| [test-checkpoints.ps1](../test-checkpoints.ps1) | 检查受管理复制的边界和保护 |
| [verify-git-checkpoints.py](../verify-git-checkpoints.py) | Python 3 标准库检查当前 Git 下载目录的阶段文件指纹；提交前加 `--staged` 检查暂存内容，防止换行转换使教材失效 |
| [verify-local-links.py](../verify-local-links.py) | Python 3 标准库检查本地 Markdown 链接与锚点；跳过长围栏代码及缓存、工作树、node_modules、dist 等非源码材料；`--output` 可保存完整 JSON 问题清单 |
| [verify-file-guides.ps1](../verify-file-guides.ps1) | 从 Markdown 代码及原文件链接恢复工程；`-Build` 增加 Go 构建，`-Frontend` 增加 Z12/Z13 的安装和前端构建 |
| [verify-search-course.ps1](../verify-search-course.ps1) | 独立数据卷中运行 Z09→Z10、检查旧任务导入、新任务同步和维护重建，结束后恢复原依赖 |
| [verify-substeps.ps1](../verify-substeps.ps1) | 顺序运行 28 个分步构建与指定测试；部分需真实依赖，`-SearchOnly` 查 Z10 三步，`-WebOnly` 从固定 Z10 查新增五步，两参数互斥 |
| [verify-checkpoint-chain.ps1](../verify-checkpoint-chain.ps1) | 从空目录运行整阶段链路，涉及实际服务与运行环境 |

## 验收证据怎么读

- [业务实现验收](../../../../verification/2026-09-10/summary.md)：业务与故障、性能的实际结果。其“下一步课程未验收/切模型”文字属于归档时状态，课程最新状态及模型偏好看工作记录。
- [整阶段链路](../verification/2026-09-10-checkpoints.md)：同一学习目录的阶段演进。
- [讲义复制](../verification/2026-09-10-lesson-copy.md)：早期复制记录，页首包含二进制复制问题的更正。
- [分步最终记录](../verification/2026-09-10-remaining-steps.md)：20 次构建、49 次指定测试执行；不是 49 个独立业务场景。
- [排错实操](../verification/2026-09-10-debugging.md)：实际错误与恢复实验。

验证只覆盖报告所述范围。只改导航页时检查链接及源码未变，不为此重复整套压测和停机演练。历史报告保留原始上下文，不把后续成绩倒填到旧报告。

链接检查在根目录执行 `python docs/learning/from-zero/verify-local-links.py --output .cache/review/local-links.json`。历史报告中的本机 `.cache` 证据若未随仓库发布，应保留其原日期与原统计，明确标注不可随新检出访问；不要用今天的检查数替换历史成绩。当前课程文件范围以 [checkpoint-index.json](../checkpoint-index.json) 与 [substep-index.json](../substep-index.json) 为准。

Git 文本统一使用 LF。阶段清单校验原始字节，因此提交前执行 `python docs/learning/from-zero/verify-git-checkpoints.py --staged`；GitHub CI 在新检出目录执行同一检查。不要通过禁用指纹检查解决换行问题。2026-09-12 发布前统一过早期快照的换行并更新指纹，未改变其业务实现或教学顺序。

## 当前前后端阶段维护顺序

结构整理、根目录迁移、搜索链路、维护重建、Z10 复制与独立学习环境验证已完成，本次整体复核见 [复核记录](../verification/2026-09-12-integrated-review.md)。搜索是新增范围，其证据单独记录，不把原有 A01—A28 验收自动算作搜索已验收。

维护最终根源码后，先检查变化归属，再按顺序运行 `publish-search-checkpoint.ps1`（验证固定 Z10）、`python publish-fullstack.py`、`publish-file-guides.ps1`、`publish-substeps.ps1`；命令位置是 `docs/learning/from-zero`，也可从根目录使用完整相对路径。不要把根源码倒灌到 Z09/Z10。Z11 引入账号与网关，必须配套发布 `internal/accounts`、`internal/platform`、`internal/app`、迁移 005、管理员 CLI 和启用账号的环境/构建脚本；Z12 引入前端与账号页，Z13 收录完整部署、持久化账号初始化和最终运行代码。

Z04—Z09 的 20 步、Z10 的 3 步、Z11 的 2 步、Z12 的 2 步和 Z13 的 1 步，合计 28 步；它们不是 28 个完全独立业务场景。`verify-substeps.ps1 -SearchOnly` 从固定 Z09 验证搜索三步，`-WebOnly` 从固定 Z10 验证新增五步。默认模式按全部阶段运行；`verify-checkpoint-chain.ps1` 当前仍是原 Z01—Z09 的真实环境验证，不能据此宣称后续阶段已运行通过。

新增阶段的完整代码、生成文件链接和锁文件必须都能恢复到学员目录；Z12-01 的测试依赖 `types.ts`、`domain.ts`、`transport.ts`、`requests.ts`、`api.ts`、`accounts.ts` 以及对应测试。账号请求测试导入 Vue，因此同一步也要发布 `package.json`、锁文件和 `frontend.ps1`，先 Install 再 Test；不能把必需依赖推迟到下一步。Z12-02 和 Z13-01 才执行完整 Vue 构建。文件复制、Node 测试、Go 测试、真实 Compose 运行和浏览器多尺寸/键盘验收分别记录，前一项通过不自动证明后一项通过。

2026-09-13 本次发布已有 13 个阶段、28 个分步、1790 条文件指纹（Z13 为 356 个文件）与 428 个完整代码块。指纹数量来自当前 `checkpoint-index.json` 的各阶段 files 总数；后续修改以重新生成的索引为准。加入账号后的完整讲义复制/构建仍在运行，不将生成成功、早期复制成功或根项目构建成功写成新一轮全部通过。

当前根工程已完成普通测试、23 项必需真实依赖门禁、30 阶段真实 HTTP 账号流程、个人任务审计/取消/CDC、30 实体场景回归，以及 29 项前端测试与构建。最新 Windows 普通测试为 [168 PASS（unit-release）](../../../../verification/2026-09-13-accounts-scale/unit-release.summary.json)，先前 Linux 普通 race 为 162 PASS；两次普通运行各有 22 项 SKIP（20 项外部依赖门控、2 项 edge/state 崩溃子进程辅助入口）；完整真实依赖 race 已由 [CI 34744143433](https://github.com/Lio6x1/meshops/actions/runs/34744143433) 验收通过，百万突发预热因消费积压未通过；教程最终复制、13 阶段构建和 5 个 Web 分步验收已完成。规模工具和教程命令已经交付，但吞吐/容量结论必须引用实际完成报告；不修改旧日期报告的数字，也不让旧“暂停”记录覆盖用户后来恢复执行的授权。

本轮正确性修复已按适用能力同步回早期阶段：例如 Z05 引入严格投影，Z08 引入历史数据库后同时更新构造器及其测试，Z09 才引入经过验证的重建恢复起点。这里的“不要倒灌”指不要把最终搜索能力整体提前复制到旧阶段，不是禁止修复阶段答案中的共同缺陷。

`verify-search-course.ps1 -FullIntegration` 在专用学习目录和 Compose 项目中完成 Z09 演示、Z10 升级/同步/索引重建，并执行学员实际使用的 `scripts/test.ps1 -Integration`（包含 ES 与指定测试门禁）。它会临时停止占用相同端口的参考依赖，结束时恢复先前运行的服务；不会删除卷。应当先停止参考应用，并避免与其他依赖故障测试并行。
