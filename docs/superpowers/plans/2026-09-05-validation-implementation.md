# MeshOps P3 工程验证与交付计划

> For agentic workers: 使用 superpowers:executing-plans 按任务执行，保持每项证据可复现。缺少依赖时记录未执行，不勾选验收。当前文档编写不启动依赖或故障注入。

**Goal:** 把已实现的六类实体与inspect闭环交付成可演示、可测量、可恢复的项目。

**Architecture:** 沿用单实例四服务及三个客户端，Prometheus记录实际指标；故障在独立测试环境运行，不新增生产管理平台。

**Tech Stack:** 现有Go测试/pprof、Docker Compose、Prometheus、GitHub Actions；etcd/PubSub/Grafana/OTel按需增强，不阻塞核心完成。

**Spec:** [命令规格](../../implementation/operations.md)、[验收矩阵与演示](../../implementation/acceptance.md)、[C05恢复边界](../../implementation/contracts.md)。

## 共同约束

V01的CLI可以随前面的功能任务逐项实现，但只有全部O04命令真实工作才完成。所有故障脚本默认只打印计划，必须显式指向专用测试环境并设置执行参数后运行；不能沿用当前脚本中的FLUSHALL或down -v去影响已有用户环境。测试样例、用例名称、运行命令、配置和原始日志应同时进入证据包。

## V01：命令、运行手册和端到端演示

**依赖：** S01—S08、T01—T04；可提前做已具备后端功能的子命令。**验收：** A24。

**Files:** 完成 `cmd/opctl/main.go`、新建 `internal/opcli/commands.go`与`commands_test.go`，完善两个模拟器CLI；更新Makefile与README已实现命令；新建 `scripts/demo.sh`、`app/task/demo_integration_test.go`。

**Interfaces:** `opcli.Run(ctx context.Context,args []string,stdout,stderr io.Writer) int`；命令解析仅接受O04参数，RPC业务复用生成客户端，不在CLI重写状态机。demo.sh使用同一manifest与token环境变量，在已有隔离测试环境运行，不自动删除数据卷。

- [ ] **先写错误输出测试。** 不带task create必需参数返回2，不能输出任务成功信息：

```go
var out, errOut bytes.Buffer
code := Run(context.Background(), []string{"task", "create"}, &out, &errOut)
if code != 2 || strings.Contains(out.String(), "taskId") { t.Fatal("invalid invocation reported success") }
```

  Run：`go test ./internal/opcli -run TestUsage -v -count=1`。
- [ ] **实现命令协议。** stdout JSON稳定、stderr诊断、token只从env名取；subscribe持续维护视图且能重连；create/get/history/cancel与各后端错误码一致。seed只对匹配注册配置幂等写入，不覆盖冲突数据。
- [ ] **验证手册实际可执行。** 从新的测试库按acceptance.md启动流程运行六类状态→订阅→inspect→结果/审计；保存commands.txt、results.ndjson、services.log。错误命令/越权操作有非零退出，禁止脚本凭最后echo判成功。
- [ ] **验收。** A24通过，移除已实现命令的占位文字；未实现可选参数明确拒绝。更新README可运行命令，保留本机/CI限制。

## V02：崩溃窗口、隔离环境与快照恢复

**依赖：** V01。**验收：** A25、A26。

**Files:** 重写 `scripts/chaos/fault_injection.sh` 成为有边界检查的真实演练工具；新增 `app/entity/recovery_integration_test.go`和 `internal/testsupport/faults_integration.go`；实现Entity `--rebuild-view`管理模式。

**Interfaces:** 测试failpoint仅通过构造参数 `func(point string) error` 或子进程专用环境开关注入，服务公网API不提供故障开关。故障点固定 after_kafka_produce/before_gateway_ack_persist、after_redis_write/before_offset_commit、after_outbox_publish/before_mark、after_inbox_result/before_report。重建模式见C05，独立reader不得提交普通projector group位点。

- [ ] **先写恢复断言。** 对20个已知实体保存期望最大版本，隔离Redis清空后重建每个都相等，包括DELETE墓碑：

```go
for id, expected := range expectedVersions {
    actual := recoveredVersions[id]
    if actual != expected { t.Fatalf("entity %s: got %d want %d", id, actual, expected) }
}
```

  expectedVersions由输入事件清单得出，不能从被测Redis反向生成。Run：`go test ./app/entity -tags=integration -run TestRebuildView -v -count=1`。
- [ ] **实现受控恢复。** 停生成并等网关积压清零，停普通Entity；捕获Kafka各分区结束水位；独立reader从保留起点重建到该水位；校验清单后原子切active，重启普通Entity从原group位点继续。老位点重放允许重复，不能用尾位点跳过未知消息。恢复中失败保留旧active，重建影子可重做。
- [ ] **故障矩阵。** A25/A26覆盖Kafka拒绝、MySQL回滚/Outbox积压、Redis查询降级、各进程强杀、缺失冷实体、取消迟到。脚本执行前核验目标container label/端口/数据库名属于独立测试环境，且不与仓库已有meshops-*容器混用；脚本未获执行标记只输出dry-run。
- [ ] **验收。** 每场景保存故障前后event/task标识、输入/输出数量、恢复时间、日志；没有输入全集不能宣称“无丢失”。只在测试环境用FLUSHDB或销毁卷，不把生产权限约定写成已验证容灾。

## V03：指标、压测和最终交付

**依赖：** V01、V02。**验收：** A27、A28。

**Files:** 新建 `internal/observability/metrics.go`和低基数标签测试；接入四服务metrics/health；实现 `scripts/benchmark/{steady_state_5k,burst_50k,subscriber_fanout}.sh`真实负载与断言；增加 `docs/results/`报告模板，更新CI检测与失败产物上传。

**Interfaces:** 指标名称遵循现有Prometheus说明，耗时_seconds histogram、累计_total counter、当前值gauge；不把tenant/entity/task/event原始ID放label。负载生成使用已有网关模式，不用Unary工具假装测双向流。

- [ ] **先写标签约束测试。** 收集服务registry的指标描述，固定标签集合只包含service/result/reason等小集合；同类10000个不同实体不生成10000条时间序列：

```go
if seriesAfter-seriesBefore > allowedFixedSeriesGrowth { t.Fatal("entity IDs caused unbounded metric cardinality") }
```

  allowedFixedSeriesGrowth由固定label笛卡尔积计算，测试数据各实体使用相同结果类型；Run：`go test ./internal/observability -v -count=1`。
- [ ] **测量而非预填。** 基线先100events/s、20实体、2订阅者，再1000/5000events/s；短时20000/50000只有低阶稳定后尝试，失败同样保存。每档预热30秒、持续120秒，报告采样时长、错误率、P50/P95/P99、Lag、CPU、RSS和恢复曲线。机器扛不住高档不伪造通过；核验发送端未先饱和。
- [ ] **脚本成为真实验收。** 缺二进制/凭证/依赖即非零退出；创建独立run-id目录，保存配置和stdout/stderr；有明确失败条件，不能打印目标QPS当实测值。当前4个guard只在对应真实实现和测试通过后移除；脚本依然默认不破坏数据。
- [ ] **最终验收。** 执行go test、go vet、staticcheck、Linux竞态、Buf生成/兼容、空库/升级迁移、integration、A01—A28。A27只要求真实基线和至少一档加压证据，不强迫消费级电脑达到全部压力目标；A28要求无未说明跳过项、全必选任务有证据。更新进度与README实际能力，保留可选增强未实现状态。

## 交付回看

完成上述计划后，另一个读者应能：看README启动→看到六类实体→订阅变化→向合法实体派发inspect→观察执行结果→重放一项故障→找到对应测试和原始指标。任何一环只能靠作者口头解释或手动修改数据库才能继续，A28不通过。
