# 09｜Redis 丢失、Kafka 保留窗口与投影世代

[返回导航](README.md) · 上一篇：[上传退出](08-upload-shutdown.md) · 下一篇：[历史清理计划](10-history-explain.md)

## 两种实际缺陷

旧普通 Entity projector 允许跳过 Kafka 保留窗口缺口，消费剩余后 lag 又可能为零；这个数字无法证明冷实体和 DELETE tombstone 都恢复了。另一个问题发生在 Redis 丢失后：新建空视图却沿用旧固定消费组，Kafka 已提交的冷事实不再重放，即使日志还完整，也可能得到“健康的空视图”。

这里的 Redis 最新状态没有物理 TTL；`expires_at` 表示逻辑新鲜度，过期快照仍可被查询为 found。不能靠等待实体自动上报或给 hash 加 TTL 来弥补历史缺口。采样历史也不是最新状态的完整权威备份。

## 修复后的恢复规则

消费组跟随 active view generation。相同世代重启沿用进度；新命名空间用新组重放。非空 TopicPrefix 同时隔离 active 指针，防止不同输入流误用同一世代；空前缀保留课程默认键。

普通最新状态投影严格检查每个 partition 的起点。新世代没有恢复豁免，起点为零。经过验证的维护重建才能提供逐 partition 的 exclusive End：重建已包含 End−1 以前的事实，普通消费可从对应 End 继续。不同 partition 的 End 不能压成一个标量。

验证标记与 active 世代一起原子发布；标记、边界与 topic 必须匹配。缺失、损坏或旧版未标记视图不获得自动豁免，可能需要显式维护重建。保留起点超过有效恢复起点时必须停止，不能消费后缀后宣告成功。世代确实改变时普通运行时退出；暂时读 Redis 失败可恢复，不能把依赖超时误认成换代。

采样 history 的丢失容忍是另一个明确边界，不能拿 latest-view 的恢复 End 证明历史样本已恢复。Search 的严格 CDC 标量接口仍保持其专用单 partition 合同，不能照搬 Entity 多 partition 边界。

## 安全诊断顺序

1. 先看服务存活、依赖连通与认证；再核对 active generation、topic、消费组是否匹配。
2. 对每个 partition 比较 Kafka beginning/end、已提交 next offset、已验证 replay floor。lag 为零只说明进度差，不证明事实完整。
3. 检查重建标记是否完整、属于该输入流、已验证并激活。保留失败标记，不手工把 complete 改成 true。
4. 确认需要重建后，遵循停止相关服务的维护流程；不在普通消费运行中切 active 指针，不把清 Redis、删 topic 或重置组当作第一步。

记录 phase、错误类别、Kafka code、topic/partition/offset、group/generation、attempt、持续时间。永久 broker 认证/配置拒绝现在向上终止消费；暂时失败仍重试同一条记录。不能为消除卡顿而跳过尚未持久成功的 offset，或把业务 payload 写进错误日志。

## 需要维护重建时怎样操作

以下命令在自己的已初始化工程根目录执行。先暂停所有来源写入，等待已确认的上传结束，记录来源侧最后版本和删除状态，再停止应用。Docker 依赖保留运行。不要从已经怀疑不完整的 Redis 反向生成 expected manifest。

```powershell
./scripts/stop.ps1
. ./scripts/env.ps1
$expectedPath = Join-Path (Get-Location) '.local/expected-state.json'
if (-not (Test-Path -LiteralPath $expectedPath)) {
    throw '先根据独立的来源记录准备完整 expected-state.json，再执行重建'
}
$newGeneration = [Guid]::NewGuid().ToString()
./bin/entity.exe -f configs/entity.yaml --rebuild-view --generation $newGeneration --expected-manifest $expectedPath
if ($LASTEXITCODE -ne 0) { throw '重建未通过；保留原视图与失败证据，暂不启动应用' }
./scripts/start.ps1
```

manifest 是 JSON 数组。每个元素包含 `tenant_id`、`entity_id`、`source_generation`、`entity_version`、`operation`（`UPSERT` 或 `DELETE`），覆盖预期的全部最新事实，包括删除墓碑。来源世代和实体身份必须与可信注册表一致，版本必须取来源侧确认的真实值。不能给全部实体随意填写版本 1，也不能只列出仍在上报的实体来掩盖冷实体丢失。教学故障驱动在生成输入时同时记录独立预期，见 [faults.go](../../internal/verification/faults.go)。

重建成功后先查询和订阅核对 `view_generation` 与预期实体，再按原来的启动选项恢复模拟器或 Search。上面的命令只启动基础应用；启用搜索的工程按 [搜索操作说明](../learning/from-zero/lessons/z10.md) 恢复。若 Kafka 保留窗口内已不含所需事实，重建应当失败：需要来源重新提供可校验的完整事实，当前工程没有凭空补回丢失数据的能力。旧版证据缺少 topic/verified 标记时，也走这一维护路径，不手工补标记绕过校验。

## 定向回归与已有证据

以下命令需要已配置的隔离 Kafka/Redis/MySQL 测试环境；逐项确认 PASS，缺依赖的 SKIP 不算验证：

```powershell
go test ./internal/bus -run 'TestStrictPerPartitionReplayBoundaries|TestStrictPartitionFloorsRejectInvalidInput|TestBrokerFailureClassification' -count=1 -v
go test ./internal/state -run 'TestNewEntityAndRunStrictRecoveryLifecycle|TestVerifiedReplayFloorsRequireActivationEvidence|TestNewRedisNamespaceReplaysCommittedLatestFacts|TestGenerationWatcherRecoversDependencyFailure|TestActiveViewIsScopedToInputTopic' -count=1 -v
```

本轮实际 NewEntity+Run 的指针丢失回归恢复了已提交冷 live 状态和 DELETE，相关组通过（10.447s）；不等长分区边界、验证标记、构造器/Run 等定向组通过（bus 4.286s、state 15.791s）；后续世代读取恢复与命名空间隔离等通过（state 5.518s）。这些是定向真实依赖证据；首轮新增 API 的编译失败只是 compile-red，不应说成已运行的缺口复现。强杀、OOM、DeleteRecords 和全量 Linux race 另由最终验收负责。

订阅修复也遵守生命周期：相同实体的版本检查共享一次元数据扫描，最后一个订阅退出时取消并等待扫描器；先筛选关注实体再克隆通知。`TestSharedReconcileReadsUniqueEntityMetadata`、`TestSharedSweepRegistrationTeardownRace` 和 `TestNotifyFiltersBeforeCloning` 覆盖这些边界。共享扫描不能用旧缓存掩盖 Redis 错误。

## Search 恢复的相邻边界

[既有 Search 重建记录](../learning/from-zero/verification/2026-09-12-search-rebuild.md)说明专用维护顺序：停止 Search/Canal，先写 incomplete 标记，建立 MySQL 一致性读视图后及时释放全局写锁，构建 ES，确认 CDC end 未改变后提交起点，最后记录完整标记及新 index UUID。失败后保持停止/未完成状态；测试脚本中的删索引步骤不是日常初始化命令。

本轮独立课程已运行 Canal/ES 同步及未完成引导、索引丢失后的恢复，见 [验收记录](../review/2026-09-12/acceptance.md)。Search 是最终一致查询；PIT 或游标过期应重新发起第一页，不能通过改游标/绕过 HMAC 延长快照。租户、筛选与页大小绑定于游标；跨页看到的快照与当前任务事实可能不同。

## 故障演练恢复成功，但容器被换了

**现象与复现。** 启用 `compose.search.yml` 后执行旧版故障演练，四项恢复报告都是成功，新增的脚本验收却报 `mysql container changed`。原因是演练只使用基础 Compose 文件调用 `up -d --wait mysql`：Compose 会按基础配置重新收敛，可能替换带搜索覆盖配置的现有 MySQL。它改变的不仅是运行状态，还有容器身份、显式 binlog 参数及日志文件连续性条件。

**解决办法。** `internal/verification/faults.go` 改用 `start --wait`，启动刚才停止的同一容器。`scripts/faults.ps1` 在执行前要求三个依赖各有一个运行实例，结束后比较容器 ID。外部配置不可变的同一实例得以保留；若容器已丢失，明确失败，不让恢复工具自行决定重建配置。需要重建时由操作者按原完整 Compose 文件组合处理，不删数据卷。

**怎样验证。** 在专用课程依赖、演示进程已停止的情况下，先记录 `docker inspect meshops-course-mysql-1 --format '{{json .Config.Cmd}}'`，执行 `./scripts/faults.ps1`，再比较配置。要求脚本退出 0、四项 `Recovered=true`，且容器身份门禁和命令参数比较都通过。仅检查 SQL 的 `log_bin=ON` 不够：MySQL 8 默认也可能启用 binlog，不能由容器替换直接断言关闭了日志。

本轮原始红绿输出在 `.cache/review/fault-preservation-red.txt` 与 `fault-preservation-green.txt`；旧版恢复报告的成功没有被改写成失败，而是新增验收揭示其未覆盖的条件。对一个不存在的隔离 Compose 项目实际执行 `start --wait`，退出 1，报告 `service "mysql" has no container to start`，未创建容器。本例是单实例故障演练工具的问题，不证明跨节点容灾能力。

实现入口：[entity.go](../../internal/state/entity.go)、[kafka.go](../../internal/bus/kafka.go)、[search](../../internal/search)、[search-admin](../../cmd/search-admin/main.go)。
