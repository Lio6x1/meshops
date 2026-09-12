# Git 发布前校验与换行修复

本记录针对首次发布完整工程和 Z00—Z10 教材。GitHub 连接已恢复，本机 Git 凭据管理器存在仓库所有者的授权记录。远端实际执行状态以 [MeshOps Actions](https://github.com/Lio6x1/meshops/actions) 中对应提交为准；本机通过不代表远端通过。

## 发现的问题与修复

提交前对比 Git 暂存内容和课程清单，800 项阶段文件记录中有 102 项指纹不一致。原因是部分原文件使用 CRLF，而 Git 入库时转换为 LF。本机直接复制检查无法发现这一差异，下载后的教材会因原始字节校验失败而停止。

已将仓库文本统一为 LF，在根 `.gitattributes` 固定检出换行，并更新阶段清单和生成讲义。早期快照仅调整换行，保留业务实现、阶段顺序和原有复制步骤。共 157 个已有文本文件发生换行统一，不重建或倒灌早期业务快照。

新增 `verify-git-checkpoints.py` 使用 Python 3 标准库验证所有阶段的原始字节指纹；`--staged` 检查实际将提交的内容。先运行该检查复现 102 项失败，修复后全部通过。CI 在新检出目录执行相同检查，文档修改也触发工作流。

## 本机实际验证

- 提交前 `go test ./... -count=1 -timeout=10m` 通过；本轮没有重新声称普通测试覆盖缺少环境变量的真实依赖场景，先前真实依赖与 race 证据另见已有报告。
- 发布清单仍为十个阶段，Z10 为 206 个文件；完整答案共 250 个文本代码块，分步共 23 步。
- Git 暂存内容的 800 项阶段文件指纹全部匹配。
- 使用 `git -c core.autocrlf=true checkout-index --all --prefix=...` 导出暂存版本，覆盖 Windows 自动换行设置下的检出场景；新目录的 800 项指纹全部匹配。
- 在该新目录运行 `verify-file-guides.ps1`，十阶段从 Markdown 完整文件代码和明确链接复制后校验通过。
- 在该新目录运行 `test-checkpoints.ps1 -AllStages`，阶段升级、重复应用、学生编辑保护、备份和路径边界通过。
- 待发布文件扫描未发现本机 `.local` 中的实际运行凭证或常见访问令牌格式；`.local`、缓存、构建产物和运行数据不进入提交。

本机导出目录为 `.cache/git-publish-20260912/checkout-e5e6473a207f4959a3f4be3265d9536f`，检查清单和早期失败记录位于 `.cache/git-publish-20260912/`。这些临时文件不上传。

先前 Linux race 报告中的网络超时是当时的实际结果；本轮重试已恢复连接。是否发布成功以及远端 CI 是否通过，需要分别核对远端提交与对应 Actions 运行，不能沿用旧骨架提交的结果。

## 首次远端运行与修复

完整工程已作为 `b5cd677` 推送到 `main`。[首次远端运行](https://github.com/Lio6x1/meshops/actions/runs/34674863353) 的新检出教材指纹、模块验证、构建和 vet 均通过；Staticcheck v0.7.0 对搜索初始化工具的两条大写开头错误提示报告 ST1005，后续测试因此尚未执行。

已将这两条错误提示改为小写开头，同步 Z10 清单、完整文件答案和分步指纹。本机使用同版本 Staticcheck 验证所有手写包通过，搜索普通测试以及十阶段讲义复制再次通过。这次仅修改错误文案，不改变业务分支或降低 CI 检查标准；后续远端结果以修复提交的 Actions 为准。

## 第二次远端运行：人工重试并发死锁

错误文案修复已作为 `ff9936f` 推送。[第二次远端运行](https://github.com/Lio6x1/meshops/actions/runs/34675321316) 通过构建、vet、Staticcheck、普通 Linux race 和依赖启动，但真实集成测试 `TestA23DurableDLQAndConcurrentManualRetry` 的十个请求中九个返回 Unavailable；搜索集成 race 尚未执行，不能把此前步骤通过当作完整验收通过。

原测试在 Windows 重复十次全部通过，在本机 Linux 重复十次时有一次复现相同失败。增加测试用的 GetTask 屏障，让十个请求都读取任务后再竞争数据库，修复前连续三次失败。InnoDB 最新死锁记录确认：一个事务持有最新投递行的 PRIMARY 记录锁、等待 supremum 上的插入意向锁；另一个事务持有该间隙锁、等待前者的记录锁。原始诊断保存在忽略的临时目录中。

修复仅将 `RetryDLQ` 的事务设为读已提交，保留 FOR UPDATE 记录锁、唯一投递 ID、唯一尝试编号和重复键拒绝逻辑；不增加进程互斥锁、不跨服务锁定任务表、不修改全局隔离级别。锁行为参见 [MySQL 8.0 事务隔离说明](https://dev.mysql.com/doc/refman/8.0/en/innodb-transaction-isolation-levels.html)。这消除本次已捕获的间隙锁循环，不承诺所有事务永远不会死锁。

修复后加强的真实 MySQL 测试在 Windows 连续五次通过，在 Linux 开启 race 连续十次通过；每次都验证十个请求正常返回、只有一个 Accepted、只新增一个重试轮次。根普通测试、Staticcheck、十阶段完整答案复制和受管理升级保护再次通过。独立复核未发现该事务调整或测试屏障的生命周期问题。

同一修复已同步到 Z07、Z08、Z09 的原任务实现，更新 Z07 讲解、阶段指纹、完整答案和分步清单。这是明确的业务缺陷修复，不向旧阶段加入搜索能力。前面所述“只统一换行”指首次发布准备，不能用于概括此后新增的事务修复。

复现与回归日志：`.cache/git-publish-20260912/retry-linux-repro.txt`、`retry-gated-red.txt`、`mysql-lock-diagnostic.txt`、`retry-gated-green.txt`、`retry-linux-race-green.txt`；完整任务模块结果保存为 `tasks-integration-final.jsonl`。这些本地临时原始记录不上传，远端完整验收仍以最终修复提交的 Actions 为准。

任务模块整体回归中 23 个顶层测试通过，故障恢复测试因本次命令未传 Kafka 地址而在环境检查处失败。补齐地址后，单独运行 `TestOutboxAndDispatcherForceKillDurableBoundaries` 通过，见 `tasks-crash-env-recheck.jsonl`。保留第一次失败记录，不把该次整包命令标为成功；以上分项补验完成后再推送修复，让远端以完整配置重新执行整套验收。
