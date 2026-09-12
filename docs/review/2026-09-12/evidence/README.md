# 2026-09-12 复核证据归档

本目录将此前保存在评审工作树中的关键结果提取为可随 Git 保存的资料。它是对已发生运行的归档核对，**不是本次重新运行测试，也不代表后续前端、网关或部署改动已经通过这些测试**。对应结论见 [验收记录](../acceptance.md) 和 [复核台账](../assessment.md)。

## 归档内容

| 文件 | 内容与核对结果 |
| --- | --- |
| [windows-integration.json](windows-integration.json) | Windows 全包真实依赖测试的逐测试终态，含子测试。顶层 145 PASS、3 helper SKIP、0 FAIL。 |
| [fresh-course-integration.json](fresh-course-integration.json) | 独立学习目录实际运行教材测试入口产生的逐测试终态，同样为 145/3/0。 |
| [linux-first-race.json](linux-first-race.json) | 首轮本地 Linux race 的历史失败，139 PASS、3 SKIP、3 FAIL；未将它替换成后续 CI 的成功。 |
| [test-summary.json](test-summary.json) | 顶层统计、包分布、缺失的必需用例、业务跳过、包括包级错误的失败列表。两个 Windows 运行均满足 19 项必需用例门禁。 |
| [course-results.json](course-results.json) | 完整 Z09→Z10 运行、十阶段完整答案复制构建、23 步运行及 Z10 三步复查。删除机器绝对路径；保留第一次 23 步运行的最终指纹失败。 |
| [source-manifest.json](source-manifest.json) | 每份源文件的相对定位、原始字节数、SHA-256，以及基线与提交关联说明。 |
| [cleanup-results.json](cleanup-results.json) | 后续安全清理实际结果：30 个目标目录已移除，逻辑文件体积合计约 3.837 GiB，记录相对路径及原始清理记录哈希。 |

逐测试记录仅有 `package`、`test`、`action`、`elapsed` 四个字段，不复制 Go 的 `Output`、时间、日志、机器绝对路径、连接串或凭证。`elapsed` 是源文件提供的秒数，缺少时为 null，不据此推断性能。顶层数量不包含名称带 `/` 的子测试、包级 `[no test files]` 或包级汇总；子测试失败仍会使门禁失败。三个允许跳过的项目都是 edge/state/tasks 包中具名的 `TestDurableCrashChild` 子进程入口。

课程记录中 `smoke=false` 仍原样保留：复制构建通过不等于每一阶段都独立启动真实服务。Z04-01、Z10-01 没有具名业务测试，仅做协议包构建，因此其 `exactTestsPassed=false` 不改写为 true。完整的业务演示由独立学习目录运行证明。

## 与已有证据对应

- [运行结果](../runtime-results.json)：故障恢复、两档压测、完整课程及追加容器配置保留结果。归档程序逐字段比较其 faults、benchmark 和 faultConfigurationPreservation.green 与原始 JSON，相等后才写出清单；不重复发布未脱敏的原始内容。
- [CI 结果](../ci-results.json)：明确记录 `a30432ab78111a852e6782d3c91a850a88bf9fb5` 对应的 [成功运行](https://github.com/Lio6x1/meshops/actions/runs/34685426074)，以及前一次代码提交的 CI。它与本地首轮 Linux 失败是不同运行。
- [SQL 实验](../../../troubleshooting/evidence/README.md)：已单独保存实验 SQL 和执行计划，不需要从缓存重建。

本地原始文件没有嵌入每次运行的精确 Git SHA，因此清单的 `localRunsExactTestedCommit` 明确为 null。基线 `641bf62`、主体修复 `31fda57`、恢复修复 `a30432a` 是根据已有验收记录关联的提交，不能当作每个本地运行的精确版本证明。SHA-256 证明日后找到的原文件是否与本次归档一致，**不单独证明运行来源可信，也不能恢复已删除的文件内容**。

追加恢复红阶段原始 JSON 仅包含四项恢复探针通过，没有包含 PowerShell 外层退出码或容器身份失败。清单明确记录这一限制；外层退出 1 来自已保存的验收记录，不能从这份 JSON 独立推导。绿阶段 MySQL 启动参数前后原始文件已逐字段核对一致。

## 生成和复核

[archive.py](archive.py) 使用字段白名单提取并计算源文件原始字节哈希，只有 Python 标准库依赖。它加载的是哈希匹配的历史门禁脚本中的用例集合，不执行 Go 构建、项目测试、Docker 或网络请求。在仓库根目录执行：

```powershell
python docs/review/2026-09-12/evidence/archive.py
```

该命令会重新写入本目录 JSON，现有 `source-manifest.json` 是历史输入的哈希依据，必须保留。对每份输入，程序先检查原 `locator`；不存在或字节哈希不匹配时，读取 `.local/archives/review-20260912/<locator>`。只有字节数和 SHA-256 都与历史清单一致才接受。这个规则同样适用于门禁脚本、`runtime-results.json` 和 `ci-results.json`，不会拿当前工程已更新的同名文件代替旧证据。两个位置都没有匹配文件时明确失败；不要为追求通过而修改历史预期。后续验收应放入新的日期目录。

例如，原输入 `.worktrees/comprehensive-review-20260912/.cache/review/windows-integration.jsonl` 的本地长期归档位置为 `.local/archives/review-20260912/.worktrees/comprehensive-review-20260912/.cache/review/windows-integration.jsonl`。清单继续保存原定位作为历史标识，复制到归档并不改变源文件哈希。

## 清理前应保留什么

待保留原始文件的精确清单就是 `source-manifest.json` 的 `sources`；其中 `locator` 从仓库根目录解析。本轮已将这些输入、旧评审文本资料、课程验收输出及旧评审工作树的 `.local` 文件复制到 `.local/archives/review-20260912/`，保持原有相对层级：共 921 份文件、6,504,033 字节，逐文件 SHA-256 核对一致。归档内 `archive-index.json` 是这批本地文件的完整索引。原始文件可能包含凭证、路径、地址和诊断内容，**整个本地归档均不可上传公开仓库**；本目录只发布经过字段白名单提取的材料。

归档后的处理边界：

1. 提交本目录及已有脱敏运行/CI/SQL 资料后，即使清理缓存，读者仍能查看逐测试结论与主要运行证据。
2. 如仍希望追查详细失败栈、启动时序或测试输出，保留上述原文件；本归档有意不保存那些内容。独立复审 Markdown 也可另作本地备份，但本目录没有把未检查的整段评审材料直接发布。
3. 大型 Go 构建缓存、可重建二进制、重复学习工程不属于证据必留集合；删除前仍需检查占用进程、当前 Git 修改、目录链接和数据位置，不能仅凭 `.cache` 名称直接整目录删除。
4. `.local` 中的凭证、数据库状态、bbolt 文件和 Compose 数据卷不在此清理授权建议内。评审工作树应先备份所需源文件，并解除仍指向主缓存的目录链接，再通过 Git 工作树命令处理。

上述归档与重建脚本本身不删除目录、不修改运行配置、不启动共享依赖，也没有重新宣称当前工程通过集成测试。后续安全清理的实际删除范围与回收量应以清理记录为准，不能从“备份完成”推导“已经删除”。

## 已完成的安全清理

归档核对后，主任务执行安全清理脚本退出 0，移除旧评审工作树、无容器挂载的 `.local/linux-go-build`，以及清理前清单中的 28 个 `course-copy-*`、`lesson-copy-*`、`substeps-*` 旧学习副本。删除前检查工作树同提交且干净、目标进程与容器挂载；先单层解除 junction，再通过 Git 移除工作树或在已核实的目录内删除文件。Git 现在只登记主工作树，旧评审分支引用保留。

30 个目标原有普通文件的逻辑大小合计 **4,119,914,506 字节，约 3.837 GiB**，清理后这些目标均不存在。此数值来自清理前逐目录记录，不等于文件系统可用空间变化；也不包含并行开发产生的新缓存。原始逐目录记录保存在 `.local/archives/review-20260912/cleanup-results.json`，其白名单字段与相对路径版本见本目录同名 JSON。

删除后再次验证本地归档 **921 个文件的字节数及 SHA-256 全部一致**。当前使用中的 `.cache/go-build-reference`、`.cache/fullstack`、主目录 `bin/`、主 `.local` 运行数据及归档均保留；Docker 数据卷未作为清理目标。**这次只清理明确列出的旧资料，没有清空整个 `.cache`。** 大型活动 Go 构建缓存留待全栈验收结束后另行判断。
