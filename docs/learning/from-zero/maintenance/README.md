# 课程维护入口（教师与 AI）

学生请使用 [课程目录](../lessons/README.md)。这里说明材料依赖关系，不是必修课程。

## 哪一份是依据

| 内容 | 依据与规则 |
| --- | --- |
| 最终业务实现 | [根目录完整工程](../../../../README.md)，module 为 example.com/meshops-course，旧框架已删除 |
| 教学顺序 | [新课程目录](../lessons/README.md) 与 [路线取舍](../../from-zero-roadmap.md) |
| 阶段文件及指纹 | [checkpoint-index.json](../checkpoint-index.json)，对应 starter、state-stages、business-stages；Z09 已固定到 business-stages/z09，新增搜索在根工程继续开发 |
| 小步骤文件及测试 | [substep-index.json](../substep-index.json)，由发布脚本维护 |
| 交付要求 | [完整代码标准](../../reference-code-standard.md) |
| 当前状态与剩余工作 | [工作记录](../BUILD-LEDGER.md)；已注明历史的段落不作当前待办 |

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
| [publish-search-checkpoint.ps1](../publish-search-checkpoint.ps1) | 根工程发布为 Z10；`-Check` 检查清单，冻结的 Z01—Z09 不改 |
| [publish-substeps.ps1](../publish-substeps.ps1) | 生成分步清单、说明和过渡文件；会写文件 |
| [test-checkpoints.ps1](../test-checkpoints.ps1) | 检查受管理复制的边界和保护 |
| [verify-git-checkpoints.py](../verify-git-checkpoints.py) | Python 3 标准库检查当前 Git 下载目录的阶段文件指纹；提交前加 `--staged` 检查暂存内容，防止换行转换使教材失效 |
| [verify-file-guides.ps1](../verify-file-guides.ps1) | 从 Markdown 代码及原文件链接恢复工程；`-Build` 增加构建 |
| [verify-search-course.ps1](../verify-search-course.ps1) | 独立数据卷中运行 Z09→Z10、检查旧任务导入、新任务同步和维护重建，结束后恢复原依赖 |
| [verify-substeps.ps1](../verify-substeps.ps1) | 顺序运行小步骤构建与指定测试；部分测试需要真实依赖 |
| [verify-checkpoint-chain.ps1](../verify-checkpoint-chain.ps1) | 从空目录运行整阶段链路，涉及实际服务与运行环境 |

## 验收证据怎么读

- [业务实现验收](../../../../verification/2026-09-10/summary.md)：业务与故障、性能的实际结果。其“下一步课程未验收/切模型”文字属于归档时状态，课程最新状态及模型偏好看工作记录。
- [整阶段链路](../verification/2026-09-10-checkpoints.md)：同一学习目录的阶段演进。
- [讲义复制](../verification/2026-09-10-lesson-copy.md)：早期复制记录，页首包含二进制复制问题的更正。
- [分步最终记录](../verification/2026-09-10-remaining-steps.md)：20 次构建、49 次指定测试执行；不是 49 个独立业务场景。
- [排错实操](../verification/2026-09-10-debugging.md)：实际错误与恢复实验。

验证只覆盖报告所述范围。只改导航页时检查链接及源码未变，不为此重复整套压测和停机演练。历史报告保留原始上下文，不把后续成绩倒填到旧报告。

Git 文本统一使用 LF。阶段清单校验原始字节，因此提交前执行 `python docs/learning/from-zero/verify-git-checkpoints.py --staged`；GitHub CI 在新检出目录执行同一检查。不要通过禁用指纹检查解决换行问题。2026-09-12 发布前统一过早期快照的换行并更新指纹，未改变其业务实现或教学顺序。

## 下一阶段

结构整理、根目录迁移、搜索链路、维护重建、Z10 复制与独立学习环境验证已完成，本次整体复核见 [复核记录](../verification/2026-09-12-integrated-review.md)。搜索是新增范围，其证据单独记录，不把原有 A01—A28 验收自动算作搜索已验收。

维护根源码后按顺序运行 `publish-search-checkpoint.ps1`、`publish-file-guides.ps1`、`publish-substeps.ps1`。原九阶段由固定快照提供；不要把根源码倒灌到 Z09。`verify-substeps.ps1 -SearchOnly` 从固定 Z09 起验证新增三步，默认模式按全部阶段运行；`verify-checkpoint-chain.ps1` 当前仍是原 Z01—Z09 的真实环境验证，不能据此宣称 Z10 已运行通过。
