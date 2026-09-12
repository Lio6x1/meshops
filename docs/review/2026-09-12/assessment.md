# 2026-09-12 全面复核台账

复核基线：`641bf62`。本轮修复、独立源码复核、Windows 真实依赖集成、教材复制/逐步操作及本地运行恢复验收已完成，详见 [验收记录](acceptance.md)。修复提交 `31fda57` 已整合到 main 并推送，[对应 CI](https://github.com/Lio6x1/meshops/actions/runs/34680625332) 的普通及真实依赖全包 Linux race 均通过。历史通过记录不能代替本轮验证。

## 原清单：28 项逐项判定

| 编号 | 判定 | 处理与证据方向 |
| --- | --- | --- |
| F1 搜索未装配 | 基线已解决，本轮端到端通过 | cmd/search、app/opctl/search-admin 与 CDC/Canal 装配已存在；独立 Z09→Z10 导入旧任务、新任务 CDC 和重建后增量同步通过。NOT_BOOTSTRAPPED 是未引导时的阻断值。 |
| F2 未投递回报 | 已修复，Windows 集成通过 | 必须存在持久化 dispatched_at；这是发送意图的证据，不能证明设备实际收到。pending 拒绝、历史尝试、取消和重复回报故障回归均已执行。 |
| F3 投影完整性 | 已修复，Windows 集成通过 | 最新视图严格消费、消费组绑定视图代次、验证重建后按分区 End 恢复并原子激活；输入 topic 隔离恢复证据。抽样历史不宣称完整轨迹。 |
| F4 消费静默失败 | 部分成立，已修复并验证 | 已补有界的阶段、原因、重试/恢复诊断及永久 broker 错误上抛。kafka-go worker 退出会触发整代重入，并非永久静默丢分区；持久化失败仍不允许跳过消息提交。 |
| F5 全量完成标记 | 基线已解决，本轮恢复通过 | 不完整标记先落，Complete/索引 UUID/位点绑定；独立学习环境缺索引及未完成引导重建通过，11 张业务表校验和保留，新 CDC 继续收敛。 |
| C1 坏命令杀执行器 | 已修复，Windows 集成通过 | 同一流上的非法命令被隔离，后续合法命令继续；磁盘/持久化错误仍失败。 |
| C2 inbox 历史扫描 | 已修复，迁移/回滚与基准通过 | pending/active 索引与事实同事务；验证主记录后重建；保留已完成幂等墓碑。启动和 Stats 仍随历史量增长。 |
| C3 取消无乐观参数 | 当前契约下不是缺陷 | 行锁下记录单调取消意图，重复请求保留首次原因，终态竞态返回事实；不承诺编辑某个快照版本。 |
| C4 订阅放大 | 已修复，Windows 集成通过 | 过滤后克隆、唯一实体版本批量核对、共享周期并绑定订阅生命周期；真实 Redis/gRPC 及注册/退出竞态回归已执行，对应提交的完整 Linux race 已通过。 |
| C5 历史清理索引 | 已实测并修复，回归通过 | 稀疏数据原查询使用 index_merge；密集样本取 500 扫 30,500，约 10.1ms。独立范围各扫 500，约 0.175/0.174ms；事务合计预算 500，保留双时钟和原子去重清理。SELECT 计划不证明实际锁数量或通用加速比。 |
| C6 凭证明文边界 | 文档已补齐 | 随机机器令牌与用户密码区分；SHA-256 只是索引；环境文件与内存仍有原令牌；loopback、无 TLS 和无用户账户能力仍是明确边界。 |
| C7 COUNT 与页不一致 | 已修复，真实 MySQL 回归通过 | 单次请求使用同一只读 REPEATABLE READ 快照；测试在 COUNT 与页查询间提交并发变更。跨页仍非长期数据库快照。 |
| C8 常量与错误码 | 已修复并验证 | platform.MaxEntityVersion 统一 Lua 精确整数上限；112 只在 Windows 下代表磁盘满；队列边界与重开回归通过。 |
| C9 审计原始幂等键 | 信息冗余已修复并验证 | 创建审计保留稳定摘要关联；不再重复复制原始键。Task 原键和旧审计不擦除，不夸大为注入漏洞或匿名化。 |
| T1 真实依赖未 race | 已补齐，云端完整 race 通过 | CI 依赖启动后完整 -race -tags integration，并检查关键测试及业务跳过。首轮 Linux 因三项基础设施/故障工具配置失败未通过；该失败记录保留；随后 `31fda57` 在云端完成全部真实依赖 race 和门禁，结论为 success。 |
| T2 ES 测试入口 | 已补本地入口与门禁 | scripts/test.ps1 的 -Integration 增加 ES 启动、环境、JSON 保存及显式验收；Windows 全包真实 MySQL/Kafka/Redis/ES 集成与 19 项门禁通过。 |
| T3 子进程空 PASS | 已修复，父测试已执行 | 三个非子进程 helper 明确 Skip；Windows 报告中的真实强杀父测试通过，helper 不计业务 PASS。 |
| T4 关键覆盖 | 新增回归与本地运行验收通过 | NewEntity/Run、连续 Upload、任务竞态及 ErrSearchExpired RPC 映射在 Windows 集成通过；独立课程完整 Canal E2E、恢复与公开 -Integration 入口通过。 |
| T5 Staticcheck 版本 | 基线已统一，本轮复验通过 | CI 与本地锁定 v0.7.0；本轮工具退出 0，Go stat-cache 写入诊断不等于 Staticcheck 失败。 |
| T6 云 CI 未运行 | 基线已解决 | `641bf62` 是历史基线；本轮 `31fda57` 的 Actions 已实际完成且成功，见验收记录中的精确链接。 |
| D1 搜索描述冲突 | 已修订 | 最终 Z10 已含搜索，Z09 有意保留加入搜索之前的能力；不将早期阶段描述套到最终工程。 |
| D2 验证清单过期 | 已更新当前状态 | 区分本轮本地通过、已确认的云 CI 与未纳入范围的能力，分别保留对应证据。 |
| D3 代码块计数 | 已区分历史与当前 | 最新发布清单合计 865 个阶段文件记录、270 个完整代码块，Z10 最终 225 个文件；记录包含跨阶段重复，不能当独立源码文件数。日期报告保留当时数字。 |
| D4 本地链接 | 当前扫描通过，最终修改后再查 | 最新全局扫描 136 个 Markdown、1,815 个本地链接、0 失效；63 个外链未请求检查。非完整 CommonMark 解析，非 Markdown fragment 与跨平台大小写仍有限制。 |
| D5 学习操作摩擦 | 已修复，分步复合验证通过 | Z08 旧测试构造器与 Z09 重建依赖复制顺序已修正；全量 23 步构建及指定测试通过，末尾 Z10 指纹失败另经刷新和 -SearchOnly 补验通过，保留原失败。 |
| D6 阶段与根差异 | 已按阶段同步并验证 | Z05—Z09 保留构造器/历史/恢复边界；完整复制 Build、升级保护和 23 步复合验证通过。Z09 README 明确仅描述本阶段，根工程/Z10 已含搜索；参考文档链接不改变阶段源文件集合。 |
| H1 新工程未进 Git | 基线已解决 | 修复已整合回主工程并推送；独立工作树保留本机验收原始资料，源码、教材及交付文档均进入 Git。 |
| H2 测试日志残留 | 已发现并移除 | 实际发现已跟踪的 internal/tasks/integration-results.txt，确认无运行依赖后移除；不能写成基线不存在。原始输出保留在忽略缓存，只发布脱敏结论和 SQL 证据。 |

## 自查新增项

- N1（已修复）：Redis 视图丢失后复用旧消费组位点，遗漏已提交的冷门实体；新世代用新组，真实 NewEntity/Run 回归恢复 cold live 与 DELETE。与 F3 一并验证。
- N2（已修复）：分发 worker 在远程查询期间取得旧 Task；Kafka 已推进 mirror 时，旧分支可覆盖终态或新增多余 attempt。第二事务锁定后重读完整 attempt 和版本；pending/dispatched 终态及仅取消版本推进的屏障回归通过。
- N3（已修复）：Inspect 大小写别名绕过重复检查，unknown-field 原错误回显 13,063 字节。精确字段校验与有界错误拒绝 alias/null/超长未知字段，规范化和幂等摘要语义保留。
- N4（已修复）：网关生成结束后丢弃上传协程的最终错误；CLI 现在等待并检查结果，正常取消与具体失败分别处理。
- N5（已修复并运行验证）：压测/故障工具同步查询 active generation 对应消费组，避免观测旧组；本轮四类故障恢复和新二进制状态链路压力测试已通过，仍不代表任务/网关/订阅容量。
- N6（已修复）：旧 inbox 的 Done=true 与未确认 PendingID 可同时存在，重建索引后报告被隐藏。启动写入前拒绝该不可能组合；回归断言错误且原文件字节不变。
- N7（已修复）：旧 inbox 留存 duration=0 的可执行命令，打开成功后毒化调度。启动验证可运行 payload，同时保留合法的取消先到边界；损坏事实 fail closed，不自动删除/改写。与 N6 的独立回归先红后绿（3.035s）。
- N8（已修复）：Upload 自身先检查 ctx.Err，使同时返回的 PermissionDenied 等具体 RPC 失败变成 context.Canceled，CLI join 无法挽回。真实 Upload 控制流屏障回归先取消再释放错误，四种明确错误保留、Unavailable/Canceled 对照仍取消；定向组通过（4.080s）。
- N9（已修复）：历史 DLQ 收到迟到 ACK/EXECUTING 后仍保持 delivery=dlq，管理员可新增误导性的 EXECUTE 重试轮次；GetTask 返回旧值时也可发生。现在同时检查权威 Task 与锁定 mirror 的执行进展，仅拒绝 EXECUTE 重试，取消重试仍允许。真实 MySQL 三个新回归先红（48.403s）后绿（含既有选择组 40.626s），随后 Windows 全包通过；原 dispatchOne 已阻止实际重复发送，本缺陷不宣称产生外部重复副作用。既有 RetryDLQ 局部 READ COMMITTED 死锁修复保留。

## 本轮证据与完成结果

| 检查 | 已知结果及边界 |
| --- | --- |
| Windows 全包真实依赖集成 | `.cache/review/windows-integration.jsonl`：145 个顶层测试 PASS，3 个具名 helper SKIP，0 FAIL；另有 17 个无测试包的 skip 事件，不计业务场景。重新运行 `python scripts/check-test-results.py .cache/review/windows-integration.jsonl` 退出 0，19 项必需测试全部通过、无业务测试跳过。不是 race 结果。 |
| 独立最终后端复核 | `.cache/review/final-backend-review.md` 对当前 bus/state/edge/tasks/platform/verification 及 CI/测试入口 diff 未发现新的可操作正确性缺陷；11 项选定回归通过（edge 5.006s、tasks 1.283s、bus 0.433s）。不包含 Search/UI/教材全面认证或任意文件损坏保证。 |
| 构建、Proto、Staticcheck | 主任务确认十个可执行文件构建、Proto lint/兼容/精确生成、Staticcheck v0.7.0 均退出 0。保存于 build-final.txt、proto-final.txt、staticcheck-final.txt；非致命 stat-cache 写入诊断不应被删去或改写成业务失败。 |
| Linux 首轮全量 race | 三项基础设施/故障工具配置失败：容器内缺 Docker CLI，及 fault-Redis 精确地址保护。未检测到 DATA RACE，但该轮不通过；随后具备工具和标准隔离端口的 `31fda57` 云端完整 race 通过，不能倒改首轮结果。 |
| 教材 | 最新完整文件复制 Build 十阶段通过，checkpoint 升级/重入/编辑保护/备份/路径边界通过。23 步构建及指定测试与 Z10 指纹补验构成完整覆盖；全量末尾失败报告不改写为成功。最新 865 条记录/270 块已生成。 |
| 独立课程运行 | `.cache/search-course-a8f6e9746a7e42eaa9e8f8c71926ddde/result.json` passed=true、cleanupErrors=[]：新 Z09 六类实体/四任务，Z10 保留凭证、导入四旧任务并同步新 CDC；缺索引/不完整引导重建保留 11 表校验和，重建后新 CDC 收敛。实际发布的 scripts/test.ps1 -Integration 与 19 项门禁通过，学习源文件摘要不变。 |
| 故障恢复 | `.cache/review/faults-final.json` Passed=true：Kafka、Redis、MySQL、Entity 进程四项故障均被观察且恢复。只证明该本地单实例演练，不扩展为集群容灾。 |
| 状态链路压力 | `.cache/review/benchmark-final.json`：10,000 实体、10 来源、3 分区，100/500 events/s 各 30 秒。500 档实际 499.9546458 events/s，ACK p99 2.925ms（15,000 样本），可见 p99 22.221ms（300 次抽样）；错误/发生器丢弃/观测遗漏均 0，最终 projector/history lag 均 0。不覆盖网关 bbolt、任务执行、订阅扇出。 |
| 发布 | `31fda57` 已推送，主目录普通测试与 865 条指纹再次通过；[本轮 CI](https://github.com/Lio6x1/meshops/actions/runs/34680625332) 全部步骤成功。此后的收尾提交只更新验收文档。 |

## 不作为 bug 扩大实现范围

模拟数据与执行方、单实例服务、本地 Compose、机器令牌、无 TLS、无生产前端、有限吞吐实验，均保留为明确边界。本轮 UI 是设计方案与模拟原型。本轮 500 events/s 档证据只覆盖状态链路，不能代表订阅扇出、网关磁盘或任务执行容量，也不是最大容量。

## 证据入口

- [本轮交付与实际验收](acceptance.md)
- [既有 Git 发布与 CI 记录](../../learning/from-zero/verification/2026-09-12-git-publication.md)
- [问题与解决办法](../../troubleshooting/README.md)
- 本轮原始临时输出位于工程 `.cache/review/`，不直接发布可能包含本机配置的原日志；最终保留可复现命令、脱敏结论及 [SQL/EXPLAIN](../../troubleshooting/evidence/README.md)。
