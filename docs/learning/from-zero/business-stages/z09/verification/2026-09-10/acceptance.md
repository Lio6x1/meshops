# A01—A28 证据索引

范围为 `docs/learning/from-zero/reference` 独立模块。用例标准沿用原 acceptance.md（原记录路径 `../../docs/implementation/acceptance.md`），不把旧骨架或课程稿的状态混入本表。下列测试名可在 [integration.jsonl](integration.jsonl) 搜索，并在 `internal/` 对应包定位源码。具名测试及其子测试全部通过，没有因缺依赖而跳过；`[no test files]` 是无测试包，不算业务验收。

| ID | 主要证据 | 核对结果 |
| --- | --- | --- |
| A01 | `TestInvalidConfigurationAndCredentialsFailWithoutLeaking`、`TestConfigDefaultsAndPath`、`TestReadinessRequiresRPCRegistrationAndDependencies` | 缺 manifest/token、重复凭证、非法容量拒绝；依赖未就绪不报 ready |
| A02 | `TestAuthBoundary`、`TestSixFixturesThroughAuthenticatedRPCAndKafka`、`TestA15A16A17TaskTransactionsAndIsolation`、`TestA19AuthenticatedSingleExecutorStreamAndDurableDelivery` | 真实 Unary/Stream 身份校验、来源伪造拒绝；两个租户同实体 ID 的快照不同，任务与回报隔离 |
| A03 | `TestA03MigrationsAndNonDestructiveSeed` | 空库及旧任务升级、重复迁移/seed、冲突 seed、旧重复审计拒绝；已包含新增004索引迁移 |
| A04 | `TestSixAdapters`、`TestStrictPerson`、`TestDroneCoordinatesAndFacilityCapacityAreValidated`、六类 RPC 测试 | 单位、时间、presence、非法字段/版本/坐标与零电量 |
| A05 | `TestWholeBatchAndPrefixACK`、[faults.json](faults.json) kafka 步骤 | 整批校验先于发布，连续前缀 ACK；真实 Kafka 停机时未误报确认 |
| A06 | `TestUploadACKLossAndCloseSendFinalACK`、`TestUploadPartialACKReplaysOnlySuffix`、`TestUploadRejectsInvalidACKWithoutDeleting`、`TestSingleSourceStreamAndPersistentRateBudget` | 丢 ACK、半关闭、双流冲突及错误水位不会导致误删 |
| A07 | `TestRedisOrderingTombstoneAndManifest`、`TestRedisForceKillBeforeOffsetReplaysWithoutRegressing` | v2/旧版本/重复/冲突语义；真实 Redis 已写、Kafka 未提交时强杀，重启重放不回退 |
| A08 | 上项、`TestRedisSubscriptionInitialRaceDeleteAndGap`、`TestRedisOOMDoesNotCommitKafkaOffset`、faults redis 步骤 | 墓碑不复活、过期仍 found、不可用不伪装不存在；真实 OOM 时位点不前进，解除 OOM 后重放 |
| A09 | `TestSixFixturesThroughAuthenticatedRPCAndKafka`、[demo.json](demo.json) | 六类均经真实 RPC/Kafka/Redis；车辆36km/h→10m/s、机器人0.65→65%、零电量/读数保留、设施2/8可查 |
| A10 | `TestRedisSubscriptionInitialRaceDeleteAndGap`、CLI 的四个订阅初始化/重连/删除测试 | 注册先于读取、并发更新保留、SNAPSHOT_END、删除与新快照替换 |
| A11 | `TestSubscriptionCoalescingAndBounds`、`TestGRPCBlockedSubscriberReleasesSenderAndDoesNotBlockPeer` | 条数/字节有界；真实 HTTP/2 慢流退出后 SendMsg 释放，正常客户端继续接收，取消清理注册 |
| A12 | `TestGatewayCLIHundredOfflineEventsRestartAndDrain`、`TestGatewayForceKillAfterRemoteACKBeforeLocalCommit` | 100条离线补传；真实远端 ACK 已到、本地未落盘时强杀，epoch/原事件保留，重传后 pending=0 |
| A13 | `TestQueueCapacityAndGenerationRollback`、`TestQueueCorruptionPreservesFile`、`TestCompactionRetainsOriginalAndAllState` | 容量不足回滚分配器，损坏保留原文件，离线压缩保持水位与事件 |
| A14 | `TestMySQLSampleIdempotencePagingAgeAndBudget`、`TestHistoryRetentionCatchesUpMoreThanOneBatch` | 抽样去重、分页/签名/租户、年龄/预算；单轮清理1500条及去重键，超过旧500条上限 |
| A15 | `TestA15A16A17TaskTransactionsAndIsolation`、`TestCreateHashUsesDeadlinePresenceNotClock` | 20并发相同创建键只有一个任务；不同内容拒绝，无显式 deadline 的重试身份稳定 |
| A16 | 同上、`TestReportRemoteValidationDoesNotHoldTaskLock` | 中途 SQL 失败共同回滚；同 expected 版本竞争最多一次推进；远程校验不持有任务锁 |
| A17 | `TestA17ListStableTimestampCursorAndTenantBinding`、`TestA15A16A17TaskTransactionsAndIsolation`、`TestExecutorReconnectBoundedWorkersAndAppliedResponseLoss` | 查询/审计真实，游标绑定租户；重复回报不重复推进 |
| A18 | `TestOutboxAndDispatcherForceKillDurableBoundaries`、`TestA18OutboxOrderFailureAndExpiredLease`、`TestA18PublishThenMarkFailureReplaysStableEvent` | 真 Kafka ACK 后强杀发布器，等待真实租约过期后重发同 event_id；顺序与旧积压保留 |
| A19 | 同强杀测试、`TestA19AuthenticatedSingleExecutorStreamAndDurableDelivery` | MySQL 已落分发事实但未提交 Kafka 位点时强杀，重放仍一个 pending attempt；断线/监听身份校验 |
| A20 | [demo.json](demo.json)、`TestStateOnlyEntitiesRejectInspectWithoutPersistingTask` | 前四类 inspect 成功，结果中的 effect_count=1；sensor/facility 返回 FAILED_PRECONDITION，无 task/history/outbox 新记录 |
| A21 | `TestExecutorForceKillRetainsSingleEffectAndExactReport`、`TestExecutorReconnectBoundedWorkersAndAppliedResponseLoss` | 真实 bbolt 结果与回报落盘后强杀，重启保留原 event_id/结果；同命令 effect_count=1 |
| A22 | `TestInboxCancelBeforeExecuteSurvivesRestart`、`TestInboxCancelCompetesWithResultTransaction`、`TestA22CancellationSuccessTimeoutBarrierAndOldEvent`、`TestA22TerminalResultAndLateAudit` | 取消墓碑、终态竞争、迟到审计；新增 `TestExecutorRestartsAfterPendingNonterminalReportExpires` 防过期回报卡死 |
| A23 | `TestA23DurableDLQAndConcurrentManualRetry` | 五次尝试、MySQL DLQ事实、通知失败后恢复、并发手工重投只有一个新轮次，终态拒绝 |
| A24 | `TestOpctlRealRPCAndProtoJSON`、CLI参数/取消/鉴权测试、[readme-walkthrough.txt](readme-walkthrough.txt) | 真实 RPC 效果、JSON与退出码；演示校验任务结果和订阅结束边界 |
| A25 | `TestKafkaShadowMissingManifestDoesNotSwitch` | 依据独立期望清单核对最新版本和墓碑，缺失时不切换，完整影子代际才激活 |
| A26 | 上项 `/actual_retention_truncation` 子测试、`TestRetentionGapIsNotReportedAsHealthyLag`、[faults.json](faults.json) | 真 DeleteRecords 删除旧事实，重建明确不完整；Lag暴露缺口，消费保留错误日志/低基数计数；三依赖停机与 Entity 强杀均完成恢复 |
| A27 | [benchmark.json](benchmark.json)、[machine.json](machine.json) | 10000个快照逐一核对，100/500两档实测；上报错误/排队丢弃/观测遗漏均0，Lag归0，指标序列46→48 |
| A28 | 本表、[summary.md](summary.md)、[quality.json](quality.json)、[linux-race.txt](linux-race.txt)、README演示与协议检查 | 必选场景有源码/输出对应关系；初始化/演示/构建/静态/竞态/协议证据可定位。逐课连续复制另行验收 |

## 证据边界

- 事务竞争、状态机、部分发布错误使用真实 MySQL 和可控 RPC/发布器控制故障窗口；不将桩执行方描述成真实硬件。新增强杀测试实际启动并杀死子进程，未用正常 Close 冒充崩溃。
- 慢流测试用较大的传输层测试帧尽快耗尽 HTTP/2 流窗口；业务字段验证由适配器与六类真实链路测试另验。它不是大字段业务准入测试或订阅扇出压测。
- 恢复测试只证明保留窗口内、期望清单明确的状态恢复。Kafka已经删除的冷实体事实不能凭空恢复；新影子不完整时保留旧视图。
- 这里按 README 命令完成工程自验；空库/升级分别由隔离迁移测试验证。未宣称已经让外部学生把整套课程逐课复制一遍。

> 归档路径说明：上述原记录路径属于当时的原工程或本机缓存；阶段副本/仓库不携带这些目标，不能将它们作为可下载附件。历史日期和计数保持不变，当前文件清单与结果请从课程目录和最新复核台账进入。
