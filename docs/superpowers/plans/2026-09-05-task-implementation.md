# MeshOps P2 可靠任务链路实施计划

> For agentic workers: 使用 superpowers:executing-plans 逐任务执行；用户明确选择委派时再使用 subagent-driven-development。先阅读Spec和S01交付，本文不授权本次自动编写业务。

**Goal:** 对指定实体完成inspect创建、可靠下发、持久执行与状态回报，并验证取消/超时竞态。

**Architecture:** Task拥有任务事实、回报、审计、Outbox；Dispatcher拥有持久投递记录与执行方流；执行方本地bbolt持有inbox/result。所有自动重试保持同一个execution_key。

**Tech Stack:** 已选Go/go-zero/gRPC、MySQL事务与条件更新、Kafka、bbolt；不引入新消息队列、任务框架或算法模块。

**Spec:** [C07—C10](../../implementation/contracts.md)、[运行与角色规则](../../implementation/operations.md)、[A15—A23验收](../../implementation/acceptance.md)。

## 共同约束

先完成S01—S04；完整六类能力验收依赖S05。S06/S07可与任务开发交错，首个可用版本仅要求人员/无人机子集演示，不能据此勾选T03完整A20或全部V01。每任务先写失败断言、运行确认，再实现、跑本任务与全量测试；测试代码是关键片段，标准imports与数据初始化根据Spec补齐。数据库事务测试必须连接真实MySQL，Kafka发布窗口必须有真实Broker测试，不能用mock宣称通过端到端可靠性。

不要手改gen。每次业务实现要替换相应Scaffold断言，并更新交接进度/证据；Git提交按用户授权。技术字段变化只在已列出的Proto与003迁移范围内实施；扩大任务类型或调度算法另行评审。

## T01：参数、状态机与任务事务

**依赖：** S01、S04。**验收：** A15、A16、A17。

**Files:** 新建 `internal/domain/inspect.go`、`task_state.go`及测试；`app/task/internal/repository/tasks.go`、`transaction.go`、`reports.go`；实现原Create/Get/List/History逻辑和ReportTaskStatus的事务入口；测试 `app/task/task_integration_test.go`。

**Interfaces:** C11的ParseInspect/NormalizePriority/CanTransition；应用层使用现有TaskService RPC。repository暴露 `Create(ctx,principal,request,entitySnapshot)(*commonv1.Task,error)`、`ApplyReport(ctx,principal,request,verifiedDispatch)(*taskv1.ReportTaskStatusResponse,error)`；参数类型为C11 Principal、现有Proto请求及GetDispatchResponse。dispatch验证RPC包装在应用层，事务中重新锁定/验证task；事务不持有跨服务网络调用。

T01回报事务可用显式构造的verifiedDispatch测试输入连接真实MySQL，证明事务、版本冲突与重复处理；此时GetDispatch后端尚未完成，RPC仍须拒绝无法校验的回报。T02/T03接通后补验A17跨服务回报与A02资源授权，不能把repository测试称为端到端鉴权通过。

- [ ] **先写纯函数失败测试。** 至少覆盖整数边界、未知参数、空note归一、默认priority和非法迁移：

```go
for _, raw := range []string{`{"duration_seconds":0}`, `{"duration_seconds":1.5}`, `{"duration_seconds":5,"shell":"x"}`} {
    if _, err := ParseInspect(raw); err == nil { t.Fatalf("accepted invalid payload: %s", raw) }
}
if CanTransition(commonv1.TaskStatus_TASK_STATUS_SUCCEEDED, commonv1.TaskStatus_TASK_STATUS_EXECUTING, false, "executor") { t.Fatal("terminal state moved backwards") }
```

  Run：`go test ./internal/domain -v -count=1`。在domain包测试中使用生成包commonv1别名。
- [ ] **实现C07原子创建。** 认证→原幂等键查找→规范化hash→首次Entity能力检查→MySQL事务pending/version1、两条初始审计、一条Outbox→返回持久结果。并发唯一键冲突读取既有结果；数据库失败返回UNAVAILABLE，不返回task_id“半成功”。
- [ ] **先写并发事务测试再实现回报。** 20个相同请求同key同时创建只出现1个task/1个创建Outbox，所有task_id相同；同key改payload为ALREADY_EXISTS。取消/完成的真实并发测试后续T04补齐，这里先验证两个回报使用同expected版本只有一个提交，另一ABORTED；回报重试同event_id返回DUPLICATE，不多写history/outbox。
- [ ] **实现查询与验收。** Get返回绑定、deadline、最终result；List游标和History顺序按C03。`go test ./app/task -tags=integration -run 'TestTaskCreate|TestTaskReport|TestTaskQuery' -v -count=1`；保存各表行数与事务回滚证据，替换Scaffold创建/回报断言。

## T02：Outbox、持久投递与执行方监听

**依赖：** T01。**验收：** A18、A19。

**Files:** 新建 `app/task/internal/outbox/worker.go`、`app/dispatcher/internal/repository/dispatches.go`、`internal/worker/consumer.go`、`internal/worker/sender.go`、`internal/executors/hub.go`（后三者位于app/dispatcher下）；实现Executor.ListenTasks、GetDispatch/GetStatus；测试 `app/task/outbox_integration_test.go`、`app/dispatcher/dispatch_integration_test.go`。

**Interfaces:** Outbox worker `Run(ctx) error`，repo `Claim(ctx,owner,limit,lease)`返回Outbox记录（ID、tenant、payload、lease token），`MarkPublished(ctx,id,owner) error`条件确认；Dispatcher repo `EnsureAttempt(ctx,task,kind,attempt,round) (*Dispatch,error)`，Dispatch字段与task_dispatches对应；hub `Attach(ctx,principal)(session,error)`维护一执行方一流。跨服务调用复用现有生成客户端。

- [ ] **先写故障窗口测试。** Outbox向真实Kafka写成功后，在MarkPublished前返回注入错误，再重启worker，允许Kafka两条同event_id，数据库最后published且只有一个task：

```go
if firstKafkaEvent.EventId != replayedKafkaEvent.EventId { t.Fatal("retry changed event identity") }
if taskCount != 1 || pendingOutboxCount != 0 { t.Fatal("outbox recovery violated task facts") }
```

  测试注入Hook为`func(point string) error`，worker选项仅供构造测试使用，point固定after_publish/before_mark；不能放公网RPC开关。Run：`go test ./app/task -tags=integration -run TestOutboxPublishWindow -v -count=1`。
- [ ] **实现C09 Outbox。** 短事务抢占、同task最小未发id、发送无数据库锁、条件标记、失败退避、租约过期重取。即使超过7天未发仍可查询处理。worker循环ctx可取消，有界等待与连接池。
- [ ] **持久分发。** Kafka收到CREATED→读取当前Task→插入确定command/attempt→提交offset；发送后掉进程不丢待确认记录；同Kafka重复不重复插入初始attempt。ListenTasks认证并绑定executor，队列满断开、不丢持久任务。GetDispatch能查历史dispatch_id给Task校验，返回数据库原记录，不能编造“最近一次”替代指定attempt。
- [ ] **验证。** `go test ./app/dispatcher -tags=integration -run 'TestDispatchPersistence|TestExecutorListen|TestDispatchLookup' -v -count=1`：无执行方仍pending、错误执行方收不到命令、落库后提交前强杀重启只有同一command；上下游停止时独立查询行为符合存储边界。A18/A19全过。

## T03：执行方持久inbox与串行回报

**依赖：** T02；完整A20另依赖S05。**验收：** A20、A21，以及A02/A17的回报集成子项。

**Files:** 新建 `internal/execution/inbox.go`、`runner.go`、`reporter.go`及`inbox_test.go`；实现 `cmd/executor-simulator/main.go`；补Task回报的内部GetDispatch鉴权；测试 `app/task/execution_integration_test.go`。

**Interfaces:** C11 Inbox API；额外 `Cancel(executionKey string) (changed bool, err error)`在同key事务写取消墓碑，`PendingReports(limit int)([]*taskv1.ReportTaskStatusRequest,error)`返回持久队列顺序，`ConfirmReport(eventID string) error`只删已确认回报。Runner依赖Clock与定时器接口，测试可以快进，但真实集成演示使用墙钟。

- [ ] **先写持久副作用失败测试。** 同命令Accept两次，结果提交两次，重开库仍effect_count=1：

```go
_, _ = inbox.Accept(command)
_, _ = inbox.Accept(command)
_, _ = inbox.CommitResult(command.Task.ExecutionKey, resultJSON)
_, _ = inbox.CommitResult(command.Task.ExecutionKey, resultJSON)
count, err := inbox.EffectCount(command.Task.ExecutionKey)
if err != nil || count != 1 { t.Fatal("duplicate business effect") }
```

  command含已绑定entity、task/executionkey与execute kind；resultJSON按C08固定。Run：`go test ./internal/execution -run TestInbox -v -count=1`。
- [ ] **实现接受/执行/回报。** 接收inbox落盘→排队ACK→GetTask读版本→ACK确认→EXECUTING确认→等待duration→事务保存唯一结果+待发SUCCEEDED→报告。断流重连取本地结果；发生时间/事件id在回报首次落盘时固定。并发≤4，任务重复不另占执行槽；超容量REJECTED也持久记住，不能下次重投变成另一次执行。
- [ ] **处理异常和恢复。** ACK或结果HTTP/2响应丢失，重发原报告；ABORTED重读后按C08判断，修改内容用新event_id。强杀发生在result提交后、回报前，重启只回报既有结果；GetTask最终result与inbox相同。
- [ ] **验收。** `go test ./app/task -tags=integration -run 'TestInspectEndToEnd|TestExecutorRestart|TestDuplicateExecution' -v -count=1`；首四类各一个inspect成功，sensor/facility创建拒绝。A20/A21通过，显示真实effectCount查询，不能仅统计客户端发送次数。

## T04：取消竞争、超时、有限重试与死信

**依赖：** T03。**验收：** A22、A23。

**Files:** 实现原CancelTask/RetryDLQ逻辑；新建 `app/task/internal/worker/timeout.go`、`app/dispatcher/internal/worker/retry.go`、`dlq.go`；补inbox取消事务，测试 `app/task/cancellation_integration_test.go`、`app/dispatcher/retry_integration_test.go`。

**Interfaces:** Task应用层 `Cancel(ctx,principal,request)`复用CancelTaskResponse；任务与投递repo内部使用事务锁/expected版本，不新增跨库原子事务。RetryDLQ使用task_id和锁定的旧dlq行作为操作幂等边界，无需扩大公开请求字段。

- [ ] **先写取消先到失败用例。** 先执行cancel落盘，再Accept迟到execute，再尝试CommitResult，副作用必须0：

```go
_, _ = inbox.Cancel(command.Task.ExecutionKey)
_, _ = inbox.Accept(command)
_, _ = inbox.CommitResult(command.Task.ExecutionKey, resultJSON)
count, err := inbox.EffectCount(command.Task.ExecutionKey)
if err != nil || count != 0 { t.Fatal("late execute bypassed cancellation tombstone") }
```

  Inbox.EffectCount对存在取消墓碑且无result返回0/nil。Run：`go test ./internal/execution -run TestCancelBeforeExecute -v -count=1`。
- [ ] **实现C09取消/终态。** 首次取消设bool+审计+Outbox，不直接宣称停止；CANCELLED要求cancel类型dispatch；timeout条件更新后迟到成功只进reports/late_result。对已终态重复取消返回固定语义，不能新建第二次cancel command。
- [ ] **实现有限重试和DLQ。** 每次等待到期持久化下一attempt/round；timeout未确认不等于业务FAILED；dlq落MySQL后Kafka通知可重发。手工RetryDLQ仅admin、未终态未超deadline受理，同旧dlq并发重投只有一个新轮次；需要重新业务执行必须创建新task。
- [ ] **真实并发验收。** `go test ./app/task ./app/dispatcher -tags=integration -run 'TestCancel|TestTerminalRace|TestRetryDLQ|TestLateResult' -v -count=1`。用屏障同时提交完成/取消确认/超时，断言唯一终态、审计版本唯一且迟到结果保留；测试配置ACK200ms、四次退避各100ms、deadline60s加快DLQ测试，记录与默认生产示例不同的配置。默认五分钟deadline可能先于自动重试上限，测试不得因尚未进入DLQ误判机制失效。
- [ ] **记录结果。** A22/A23全过后P2完成；仍无自动最优分配、路径规划或多实体联合作业。
