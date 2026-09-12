# 11｜本机通过不等于新检出或集成 CI 通过

[返回导航](README.md) · 上一篇：[历史执行计划](10-history-explain.md) · 下一篇：[机器 token](12-machine-token.md)

## 案例一：文本看起来相同，原始字节摘要却不同

首次发布前，800 条课程阶段文件记录里有 102 条与 Git 暂存内容不符。本地源文件部分是 CRLF，Git 入库转 LF；本地直接复制成功不能证明学生从 Git 新检出后成功。

修复是在 `.gitattributes` 固定文本 LF，统一 157 个已有文本文件的换行，按实际入库字节更新阶段清单与讲义。早期业务快照没有因此重建为最新实现；后来另行同步的 RetryDLQ 是明确的业务修复，两件事需要区分。

安全检查命令：

```powershell
python docs/learning/from-zero/verify-git-checkpoints.py
python docs/learning/from-zero/verify-git-checkpoints.py --staged
```

第一条检查当前文件，第二条读取已经暂存的内容；不会替你暂存。检查失败时先比较 Git 内容和工作区换行，不要直接把失败摘要填回清单来消除告警。真正的源文件修改还需要完成对应阶段复制与升级保护验证。

历史证据为先红 102 条、修复后 800 条全部匹配；另外用 `core.autocrlf=true` 从暂存区导出到独立新目录，800 条仍匹配，并完成十阶段讲义复制及升级、重复应用、学生编辑保护、备份/路径边界测试。不要用覆盖当前工作区的检出来验证这一点。详见[原始发布记录](../learning/from-zero/verification/2026-09-12-git-publication.md)。

## 案例二：Staticcheck 报 ST1005，测试还没有开始

首次远端提交 `b5cd677` 的构建、vet、教材指纹通过，但 Staticcheck v0.7.0 指出 search-admin 两条错误文案大写开头。流水线在此停止，后续测试尚未执行，不能写成“业务测试失败”，也不能把前面的绿色步骤当成整条 CI 通过。

修复只把错误文案改为小写，保持检查规则，同步 Z10 清单/答案/指纹。本机同版本 Staticcheck 手写包检查通过。已安装该版本时可先核对版本，并运行限定包诊断：

```powershell
staticcheck -version
staticcheck ./cmd/search-admin ./internal/search
```

这只是定位入口；完整手写包范围以 [CI 工作流](../../.github/workflows/ci.yml) 为准。随后 `ff9936f` 的远端运行通过 Staticcheck，却在真实 RetryDLQ 并发测试发现死锁，见 [02](02-retry-dlq-deadlock.md)。保留每次运行的 commit、run ID、失败阶段，不能把几个不同版本的分项绿色拼成一个完整绿色提交。

## 案例三：go test 返回 0，依赖场景却 SKIP

[历史 Linux race 记录](../learning/from-zero/verification/2026-09-12-linux-race.md)中，普通根工程 race 有 84 个顶层 PASS、12 个依赖类 SKIP、0 FAIL；另一次真实搜索 race 有 19 个顶层 PASS、0 SKIP、0 FAIL。前者不覆盖那 12 个外部依赖路径。Windows 当时 `CGO_ENABLED=0` 无法运行 race；最初 Linux 离线缓存缺依赖导致编译失败，也不是竞态测试失败。

当前工作流已安排带真实依赖的全量 integration race，并用 [check-test-results.py](../../scripts/check-test-results.py) 检查 JSON 事件：必需测试必须按包名与测试名明确 PASS；失败、缺失及业务测试 SKIP 都拒绝。普通不带依赖的单元测试不能套用这个门禁。

已有明确集成报告可只读检查：

```powershell
python scripts/check-test-results.py results/integration-race.jsonl
```

报告必须来自对应提交的一次完整显式集成运行，不能用定向报告冒充。运行管道时还要保留 `go test` 非零退出码，避免 tee/重定向掩盖编译或测试失败；工作流采用 pipefail。原 JSON 可能含本机配置，不要直接发布未脱敏日志。

## 子进程 helper 的 SKIP 是正确口径

edge/state/tasks 的 `TestDurableCrashChild` 在父测试之外不做业务场景，应显式 SKIP；未知的非空 crash mode 应 FAIL。门禁只豁免这三个包中这个**精确 helper 名称**，不会豁免实际强杀父测试或其他子测试。

任务 helper 本轮单独运行显示 SKIP、包 PASS（2.391s）；这不代表强杀场景通过。父测试仍需要确认子进程到达指定持久化屏障，真正杀掉它，再验证恢复事实。历史整包任务回归有一次缺 Kafka 配置失败，补配置后的单独强杀父测试通过；两份证据都保留，不能把第一次整包结果改成成功。

截至本知识库编写时，本轮引用的是各模块定向结果和已有历史验证；新增全量 Linux race、教材最终同步和远端提交的最终状态请以各自新报告为准，本页不预先宣告通过。
