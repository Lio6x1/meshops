# 全面复核：交付与验收记录

本轮基于 `641bf62`，处理用户给出的 28 项检查意见，并独立检查现有业务、装配、测试、脚本和教材。逐项判定与额外发现见 [复核台账](assessment.md)。本机最终运行验收及修复提交 `31fda57` 的[云端 CI](https://github.com/Lio6x1/meshops/actions/runs/34680625332) 均通过，不用历史 CI 代替。

## 四项交付

1. 修复投递意图校验、严格投影恢复、消费错误传播、订阅核对、历史清理、任务并发和本地执行队列等问题。旧报告中的搜索装配、完成标记、Git 状态等部分内容已被基线实现解决，按现有代码复核，没有重复造一套实现。
2. 为八个手写核心包补充职责和调用流程说明，并在事务、版本、并发、协程退出、恢复证据和关键测试处补充必要注释。协议生成代码未手改。
3. [排错知识库](../../troubleshooting/README.md) 保存 12 类实际问题，包括 RetryDLQ 死锁、事务快照、坏 inbox、上传取消、投影恢复、历史清理执行计划和 Git/CI。SQL 实验有可复现脚本与原始执行计划，不把“扫描行数”解释为已测量的锁数量。
4. [UI 设计](../../ui/design.md) 与 [离线交互原型](../../ui/prototype/index.html) 展示六类实体、任务、分发和搜索。它使用明确标注的模拟数据；正式浏览器接入的 BFF、授权实体列表等接口另列为待实现设计。

## 构建、回归和独立复审

| 检查 | 本轮结果 |
| --- | --- |
| 构建与协议 | 十个可执行程序构建通过；Proto lint、兼容性与逐字节重新生成通过。 |
| 静态检查 | Staticcheck v0.7.0 退出 0；手写 Go 文件 gofmt 检查无差异。部分命令出现非致命的 Go stat-cache 写入权限诊断，原输出保留。 |
| Windows 全包集成 | `go test -tags integration ./... -json -count=1 -timeout=15m` 通过：145 个顶层测试 PASS、3 个具名子进程 helper SKIP、0 FAIL。19 项必需用例门禁通过，无业务测试跳过。 |
| 门禁自身 | 完整、空报告、缺少必需测试、业务跳过、无测试包、允许的 helper、子测试失败七种输入符合预期。没有把 `[no test files]` 包当作业务跳过。 |
| 独立最终代码复审 | 对 bus/state/edge/tasks/platform/verification 和 CI/测试入口修改未发现新的可操作正确性缺陷；另运行 11 项定向回归通过。Search 由主审查检查并纳入实际课程运行。复审不构成无缺陷保证。 |
| Linux 首轮 race | 未通过：Go 容器内缺少故障测试所用 Docker CLI，另一个测试因 fault-Redis 地址保护拒绝执行。未检测到 DATA RACE，不等于完整 race 通过。随后 `31fda57` 在具备 Docker CLI 和标准隔离端口的 GitHub CI 完成普通 race、全包真实依赖 race 和门禁，[全部成功](https://github.com/Lio6x1/meshops/actions/runs/34680625332)。首轮失败仍保留。 |

Windows 顶层 PASS 分布为：edge 37、tasks 33、state 27、search 20、cli 10、bus 7、platform 6、verification 3、app 1、cmd/verify 1。三个 helper 仅负责被强杀父测试启动的子进程，不计入业务通过数；无测试包也不计测试用例。

## 教材复制与逐步操作

- 十个阶段的完整文件答案复制、构建通过；阶段升级、重复应用、编辑保护、备份和路径边界检查通过。
- 全量分步运行实际执行了 23 次构建和各步指定测试。发现 Z08 构造器升级时漏同步测试、Z09 重建辅助文件引入顺序问题，修正发布清单后通过。
- 该次全量分步最后发现 Z10 验收脚本修改后未刷新指纹；重新发布并从 Z09 独立验证 Z10 三步，构建、测试和阶段末指纹全部通过。不是把失败的全量报告改为成功。
- Z04-01、Z10-01 是协议生成包构建步骤，本身没有业务测试；其他步骤必须出现清单中的指定测试 PASS。
- 本轮发布为 270 个完整文件代码块、865 个阶段文件条目，最终 Z10 含 225 个运行及配套文件。当前数量由 `checkpoint-index.json`、`substep-index.json` 和复制验证器提供，历史报告保留历史计数。

## 真实运行、恢复与性能

独立学习运行 `search-course-a8f6e9746a7e42eaa9e8f8c71926ddde` 完成以下五项，驱动退出 0、`passed=true`、无清理错误：

1. 从空目录依次应用阶段，在专用 Compose 项目初始化 Z09；六类实体可查询，四类执行方完成四个 inspect 任务。
2. 升级 Z10 保留凭证文件字节；旧任务导入搜索，新任务经过 MySQL→Canal→Kafka→Search→ES 收敛。
3. 未完成标记与 ES 索引丢失后重建成功，11 张业务表校验和不变；恢复后新任务继续同步。
4. 直接运行教材中的 `scripts/test.ps1 -Integration`：普通测试、vet、完整真实依赖测试及 19 项必需测试门禁通过。
5. 学习副本的发布文件哈希未被初始化、运行或测试改变；停止专用应用和依赖，恢复先前运行的参考依赖，没有删除数据卷。

当前二进制另完成 Kafka、Redis、MySQL 停机和 Entity 进程退出四项恢复，全部 `Recovered=true`。程序报告的恢复耗时分别约 36.30、4.00、4.76、28.67 秒，取决于本机容器启动、RPC 超时与重连；不是生产 RTO 承诺。

Windows/amd64、Go 1.25.10、24 个逻辑 CPU，10,000 实体、10 个来源、3 个分区，预热 10,000 条并确认 10,000 快照后，对 100 和 500 events/s 各运行 30 秒：

| 输入速率 | 实际受理吞吐 | Kafka ACK p99 | 生成到观察 p99 | 观察样本 |
| --- | --- | --- | --- | --- |
| 100 events/s | 99.990 events/s | 2.782 ms | 12.303 ms | 60 |
| 500 events/s | 499.955 events/s | 2.925 ms | 22.221 ms | 300 |

两阶段均无发生器队列丢弃、受理错误或抽样观察遗漏，结束时最新视图与历史消费 lag 均为 0。观察延迟包括轮询延迟，每 50 条计划事件抽取一个样本，看到同版本或更高版本即算观察成功。它覆盖独立 Ingest→Kafka→Entity→Redis/MySQL 状态链路，排除网关 bbolt、任务执行和订阅扇出，不能称为最大容量或全平台吞吐。脱敏结构化结果见 [运行证据](runtime-results.json)。

历史清理另在隔离数据库生成十万条记录进行 EXPLAIN ANALYZE。稀疏样本原查询会使用 index_merge；积压样本 `OR + ORDER BY id + LIMIT 500` 扫描 30,500 行，约 10.1ms，两个独立年龄范围查询各扫描 500 行，约 0.175/0.174ms。生产实现把两段放在同一事务，合计删除预算仍为 500，并原子清理去重键。这是指定分布上的读取计划实验，不是生产容量或固定加速比承诺。见 [SQL 证据](../../troubleshooting/evidence/README.md)。

## Git 与云端验证

修复提交 `31fda577d2269f319f2d51deabf454e8f2aa3b36` 已快进整合到主目录并推送。主目录再次执行普通测试和 865 条教材指纹检查通过，十个可执行程序已重新构建。[GitHub Actions](https://github.com/Lio6x1/meshops/actions/runs/34680625332) 的全部步骤成功，包括模块校验、构建/vet、Staticcheck、普通 race、启动真实依赖、全包 integration race 及指定测试门禁；脱敏响应见 [CI 记录](ci-results.json)。

最终文档提交记录以上已经发生的结果，不修改业务代码。临时网络连接问题与单次 DNS 解析处理已纳入 [Git/CI 排错](../../troubleshooting/11-git-ci.md)。主目录与远端最终提交会在交付前再次核对。

## 如何复验

在本机课程工程、已安装所需工具且测试依赖可用时执行：

```powershell
./scripts/build.ps1
./scripts/verify-proto.ps1
./scripts/test.ps1 -Integration
python docs/learning/from-zero/verify-git-checkpoints.py
./docs/learning/from-zero/verify-file-guides.ps1 -Build
./docs/learning/from-zero/test-checkpoints.ps1 -AllStages
```

23 步依赖测试先按 [分步接线说明](../../learning/from-zero/lessons/steps/remaining.md) 配置测试环境，再运行 `verify-substeps.ps1`。独立 Z09→Z10 演示、重建和公开测试入口由 `docs/learning/from-zero/verify-search-course.ps1 -FullIntegration` 验证；该驱动会临时腾出参考依赖的固定端口，结束时恢复原先运行的服务，不能与依赖故障测试并行。Linux CI 执行完整 `go test -race -tags integration ./...` 和必需测试门禁；结果见 [GitHub Actions](https://github.com/Lio6x1/meshops/actions)。

本机原始输出位于本轮工作树 `.worktrees/comprehensive-review-20260912/` 内：审查输出在 `.cache/review/`，独立学习运行在 `.cache/search-course-*`，故障/压测配置和进程日志在 `.local/verification/`，均不作为教材源码发布。复核结论、问题说明、脱敏运行结果和 SQL 证据已保存在本目录和知识库，随主项目提交。不要提交 `.local` 凭证、bbolt 文件或整段未检查日志。

## UI 验证与能力边界

原型 JavaScript 语法与非浏览器交互测试通过，覆盖导航、详情、筛选、创建、取消意图、管理员重试、搜索过期、错误恢复和输入转义。已静态检查三个响应式断点。浏览器工具拒绝本地预览访问，因此实际视觉、键盘焦点和响应式截图未验收，没有通过其他入口绕过。用户可直接打开本地 HTML 评审。

本项目仍是模拟来源和执行方、单实例、本地 Compose、机器令牌与 loopback 明文。完成幂等墓碑没有自动归档；采样历史不是完整轨迹；操作员选择实体后可靠下发并非路径规划或资源优化。详见 [工程边界](../../production-readiness-checklist.md)。

## 收尾追加：容器配置恢复验收

主目录启动补查时先发现参考容器全部停止（Exited 255），启动脚本明确报 MySQL unavailable 并清理应用进程；该记录本身不能证明代码缺陷或确定 Docker 退出原因。恢复依赖后，进一步确认已有故障工具使用基础 Compose `up` 可能替换带搜索覆盖参数的 MySQL。

新增身份门禁在旧代码上退出 1；修复为 `start --wait` 后，实际运行 `f83f6ece0cc24a7a978c1bedcfaa93ac` 四项均恢复，脚本退出 0，Kafka/Redis/MySQL 容器身份与 MySQL 命令参数保持不变。恢复耗时约 35.27、4.25、4.37、29.48 秒。另测不存在的隔离容器，start 明确退出 1，不创建容器。原来的四项通过仍保留，但不能据此宣称当时覆盖了配置保留。

N10 已同步 Z09、最终 Z10 的源文件和完整答案；必要注释说明为何不能用 up。此追加修改故障工具，新的提交应重新通过 CI；历史 `31fda57` 和文档提交 `cfcae97` 的成功不能代替新提交结果。提交对应状态可在 [Actions](https://github.com/Lio6x1/meshops/actions) 核对，精确运行链接随最终交付给出。
