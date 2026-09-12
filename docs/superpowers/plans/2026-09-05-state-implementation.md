# MeshOps P0—P1 状态链路实施计划

> 按日期保留的设计/实施记录，不作为当前待办或启动指南。当前工程已演进至 Z13；旧路径和阶段状态按当时上下文理解，后续状态见 [记录说明](../README.md)。

> For agentic workers: 按任务逐项执行；有对应技能时使用 superpowers:executing-plans。只有用户选择委派时才使用 subagent-driven-development。本文是后续业务开发计划，本次编写文档不执行这些任务。

**Goal:** 两类来源先形成可运行状态链路，再完成六类实体、可靠补传、订阅和历史抽样。

**Architecture:** 保留Ingest/Entity/Task/Dispatcher四服务。状态功能落在Ingest和Entity；来源转换、队列与CLI为独立本地包/客户端。

**Tech Stack:** Go/go-zero/grpc-go/Protobuf、franz-go、go-redis/v9、database/sql+mysql、bbolt；真实存储只在integration测试使用。

**Spec:** [交付契约](../../implementation/contracts.md)、[运行规格](../../implementation/operations.md)、[验收矩阵](../../implementation/acceptance.md)。

## 共同约束与执行方式

- 先读Spec再实现；路径均相对仓库。本文新增文件在任务完成前可以不存在，现有文件不得全量覆盖。
- 每任务先写给定断言并运行确认失败，再实现，再运行对应测试和 `go test ./...`；真实依赖测试必须有 `//go:build integration` 标签。未发现匹配测试不能算PASS，使用 `go test ... -list` 先确认测试名。
- 测试代码块是关键断言片段，测试文件还需添加标准imports与初始化；完整输入/预期见验收ID。局部类型与接口在C11及各任务Interfaces定义，允许拆内部文件但不变更业务契约。
- 不为了测试通过保留空成功；完成业务接口时替换已有Scaffold断言。优雅退出等纯函数测试之外的行为必须通过实际RPC/进程验证。
- 不改001/002历史迁移，不手改gen；新库加入go.mod的固定版本，升级已有核心依赖须先证明需要。完成后只更新交接进度与证据，Git提交按用户指令。

## S01：可验证的配置、身份、元数据与协议增量（P0）

**依赖：** 当前骨架。**验收：** A01、A03及A02身份/角色子项；A02资源隔离随S03/S04/T01/T03完成。

**Files:** 新建 `internal/auth/manifest.go`、`internal/auth/interceptor.go`、`internal/runtimeconfig/config.go`、`internal/bootstrap/seed.go`；修改 `app/*/internal/config/config.go`、`app/*/internal/svc/servicecontext.go`、四个main、`app/*/etc/*.yaml`、`cmd/opctl/main.go`、Proto中C07/C08字段；新建 `migrations/003_implementation_contracts.sql`、测试 `internal/auth/manifest_test.go`、`app/task/migration_integration_test.go`。

**Interfaces:** `auth.LoadManifest(path string, getenv func(string) string) (*auth.Manifest,error)`、`Manifest.Authenticate(token string)(auth.Principal,error)`；`runtimeconfig.CommonConfig`字段对应O01表，`LoadCommon(path string)(CommonConfig,error)`完成YAML/环境覆盖与验证。ServiceContext构造改为可返回error并持有本服务客户端和关闭函数；别在库内Fatal退出。`bootstrap.Seed(ctx, manifest, db, kafkaAdmin) error`参数为已解析manifest、*sql.DB及franz-go admin客户端；只镜像配置，不自动覆盖冲突。

- [ ] **先写失败用例。** 创建临时manifest，将两个actor的credential_env映射到同一测试token；必须拒绝，另外测试缺token和未知role：

```go
_, err := auth.LoadManifest(path, func(string) string { return "0123456789abcdef0123456789abcdef" })
if err == nil { t.Fatal("duplicate credential across principals must be rejected") }
```

  Run：`go test ./internal/auth -run TestManifest -v -count=1`，开始应失败，不能略过重复token情形。
- [ ] **按O01/O02实现。** 解析→范围验证→身份唯一性→文件相对路径→环境密钥→启动元数据比对；Unary/Stream同一principal解析，业务层继续做资源绑定。seed冲突报字段，不泄露值中的凭证。静态客户端懒连接避免Task/Dispatcher启动环。
- [ ] **实现契约增量和迁移。** C02的既有测量字段optional化、C07的Task结果字段、C08的GetDispatch字段、C10的003完整DDL及task_dispatches的dlq字段/枚举一起落地；C02的三个新增类型组件由S05完成。运行`bash scripts/proto-gen.sh all`；补app wrapper转发，原业务仍UNIMPLEMENTED。CI同时执行空库001→003和有旧任务002→003，断言业务ID/Outbox保留及DDL冲突拒绝。
- [ ] **验收。** 运行身份单测、迁移integration测试、Buf lint/breaking、go test/vet；缺DB时状态为待集成验证。记录A01/A03和A02身份子项结果，明确A02资源隔离尚待后续业务集成；S01完成不代表A02全项通过。

## S02：人员与无人机确定性适配（P0）

**依赖：** S01。**验收：** A04。**Files:** 修改 `internal/integration/person/adapter.go`、`drone/adapter.go`；新增 `internal/integration/validation.go`、两类`adapter_test.go`；使用已有 `testdata/sources/person.json`、`drone.json`与normalized-expectations。

**Interfaces:** 保留 `Adapter.Normalize(context.Context, Binding, Input)(*commonv1.EntityStateEvent,error)`；Binding仅可信映射，时间/目录归一按照C01/C02。公共校验函数返回可识别错误，不依赖grpc状态码，RPC层映射INVALID_ARGUMENT。

- [ ] **先写失败用例。** 在drone包读取fixture（从该包目录相对`../../../testdata/sources/drone.json`），调用`Adapter{}`：

```go
event, err := (&Adapter{}).Normalize(context.Background(), binding, input)
if err != nil { t.Fatal(err) }
if event.Snapshot.Location.Longitude != 121.47 || event.Snapshot.Power.GetBatteryPercent() != 78 { t.Fatal("incorrect unit normalization") }
if event.Snapshot.Person != nil { t.Fatal("drone must not acquire a person component") }
```

  binding设demo_tenant/drone_sim/generation1、映射drone-001；input为fixture和2026-09-05T00:00:01Z。Run：`go test ./internal/integration/... -run TestNormalize -v -count=1`。
- [ ] **实现纯转换。** 严格JSON解码、必填presence、类型与值域、ID映射、单位与UTC时间；生成UPSERT、schema1、expires_at=occurred+30s，目录由可信注册配置补入中心视图，不能信任来源新增目录。源payload无效不产生半个事件。
- [ ] **补完整边界表。** A04列出的缺字段、0读数/0电量、NaN等、负版本、未知字段、越界经纬度、重复技能分别断言成功或失败。重复同input输出除received_at外完全相同。人员必须无Power，缺定位整组件nil。
- [ ] **验收。** 全适配测试通过；更新S02记录。此时只完成转换，不写“接入Kafka已通”。

## S03：真实Kafka受理与双向累计ACK（P0）

**依赖：** S01、S02。**验收：** A05、A06。**Files:** 新建 `app/ingest/internal/producer/kafka.go`、`session.go`；实现原 `reportentitystateslogic.go`，接入Stream鉴权；测试 `app/ingest/ingest_integration_test.go`、`app/ingest/internal/producer/session_test.go`；删除/替换原Scaffold上报断言。

**Interfaces:** producer层 `Publish(ctx context.Context, events []*commonv1.EntityStateEvent) []error`，结果长度与输入一致，nil只表示该条被Kafka确认；session `Apply(firstSequence int64, results []error) (confirmed int64, err error)`，构造时带首批resume水位，保存连续前缀。Kafka事件不绑定业务成功响应。

- [ ] **先写失败用例。** 在producer包构造水位0的session，将[成功,失败,成功]按序输入：

```go
confirmed, err := session.Apply(1, []error{nil, errors.New("broker unavailable"), nil})
if err == nil || confirmed != 1 { t.Fatalf("ACK crossed a gap: %d %v", confirmed, err) }
```

  `go test ./app/ingest/internal/producer -run TestContiguousACK -v`；另用真实Kafka制造ACK丢失后重发，断言消息可重复而ACK不越界。
- [ ] **实现C04。** 整批认证与校验通过才Produce，配置acks=all与幂等生产；按条最终结果推进前缀；部分失败先送错误帧再结束流。每来源一流、单批在途、限流先于持久化，流退出释放名额。临时Kafka错误不标成功。
- [ ] **集成测试。** `go test ./app/ingest -tags=integration -run 'TestIngest' -v -count=1`：有效批、非法整批、部分失败、断流、CloseSend最终ACK、同来源双流、跨租户上报。测试从Kafka独立reader核对值和Key，不能只mock producer。
- [ ] **验收。** A05/A06全过；上报RPC原UNIMPLEMENTED测试由上述结果断言替换。记录Kafka版本/分区和测试输出。

## S04：Redis原子投影与快照查询（P0）

**依赖：** S03。**验收：** A07、A08。**Files:** 新建 `app/entity/internal/projection/projector.go`、`snapshot.lua`、`app/entity/internal/repository/snapshots.go`，实现GetSnapshot/BatchGetSnapshots及svc初始化；测试 `app/entity/projection_integration_test.go`。

**Interfaces:** `SnapshotRepository.Apply(ctx,event)(ApplyResult,error)`，ApplyResult枚举Applied/Duplicate/Stale/Conflict；`Get(ctx,tenant,entity)(*entityv1.GetSnapshotResponse,error)`；构造传active generation并限制tenant；projector消费每分区串行、有界并发，错误不越过位点。结果快照里的executor_id及目录读取注册事实，不取遥测提供值。

- [ ] **先写失败用例。** 在真实Redis隔离命名空间依次写入同实体v2、v1、相同v2、同v2不同内容：

```go
for _, e := range []*commonv1.EntityStateEvent{v2, v1, v2, conflictV2} { _, _ = repo.Apply(ctx, e) }
snapshot, err := repo.Get(ctx, "demo_tenant", "drone-001")
if err != nil || snapshot.Version != 2 || snapshot.Snapshot.Location.Longitude != v2.Snapshot.Location.Longitude { t.Fatal("projection regressed or accepted conflict") }
```

  数据初始化必须按C01配置来源；Run：`go test ./app/entity -tags=integration -run 'TestProjection|TestSnapshot' -v -count=1`。
- [ ] **实现C05。** Lua读比较写同Hash；逻辑过期和删除墓碑；unknown类型/来源冲突隔离；Kafka处理完或明确拒绝才commit；RedisOOM/断开停止该分区推进，Get返回UNAVAILABLE。
- [ ] **测试位点安全。** 在Redis写完、Kafka提交前杀Entity，重启从未提交记录继续，快照仍v2；DELETE v3后重放v1/v2不能复活；不存在found=false、过期found=true带expiresAt；跨租户相同ID独立。
- [ ] **验收。** 原查询占位被真实查询替换，A07/A08通过；readiness可反映Redis失败，不能无界回源MySQL。

## S05：其余四类实体接入（P1）

**依赖：** S02、S04。**验收：** A09。**Files:** 新建 `internal/integration/{vehicle,robot,sensor,facility}/adapter.go`及各`adapter_test.go`，新建`internal/integration/registry.go`，修改C02 Proto组件与simulation配置，保留已有4份新原始fixture。

**Interfaces:** `integration.NewAdapter(kind string)(Adapter,error)`，精确返回六类实现，未知返回错误；Normalize与S02相同；新增组件号按C02，缺测量用presence，metadata不替代已定义的载荷/占用字段。

- [ ] **先写失败用例。** table包含6个fixture及预期值；传感器读数0必须存在，设施occupied0合法，传感器/设施目录为空：

```go
if snapshot.GetSensor().Reading == nil || snapshot.GetSensor().GetReading() != 0 { t.Fatal("zero reading was lost") }
if len(snapshot.GetTaskCatalog().GetDefinitions()) != 0 { t.Fatal("passive entity acquired task capability") }
```

  上述sensor输入取sensor fixture并把measurement.value改0；Run：`go test ./internal/integration/... -run TestNormalize -v -count=1`。
- [ ] **实现。** C02四种单位/字段映射，车辆36km/h=10m/s，机器人energy.ratio0.65=65%；同一source内必须匹配adapter类型；source能力不能替代注册限制。重新生成gen并补所有组件presence验证。
- [ ] **集成演示6类。** `go test ./app/entity -tags=integration -run TestSixEntityTypes -v -count=1`：逐类走Ingest→Kafka→Redis→GetSnapshot，验证类型、专有字段与缺省组件；同一Entity服务实例处理全部6类。
- [ ] **验收。** A09通过且六类样例/预期结果一致；技术说明不宣称实现路径规划、硬件控制或行业管理模块。

## S06：订阅同步、增量合并与慢端治理（P1）

**依赖：** S04；六类订阅验收还依赖S05。**验收：** A10、A11。**Files:** 新建 `app/entity/internal/subscription/hub.go`、`subscriber.go`及`hub_test.go`，实现Subscribe逻辑；测试 `app/entity/subscription_integration_test.go`。

**Interfaces:** `Hub.Register(ctx,tenant,ids)(*Subscription,error)`；Subscription保存syncID、viewGeneration、有界pendingMap和发送队列，`Close()`幂等；`Hub.Notify(tenant,id,version,generation)`只通知本租户，版本值不作为跨实体游标。

- [ ] **先写失败用例。** 精确暂停在“登记完成、快照读取未完成”处写v2，完成初始化后客户端必须最终v2：

```go
if !sawSnapshotEnd { t.Fatal("missing synchronization boundary") }
if clientView["drone-001"].Version != 2 { t.Fatal("update between registration and snapshot was lost") }
```

  测试用channel barrier控制时序，不依赖随机sleep。Run：`go test ./app/entity -tags=integration -run TestSubscribeNoGap -v -count=1`。
- [ ] **实现C05算法。** 先登记/缓冲→快照→END→排空→实时；最终视图按新syncID替换；合并保留最新DELETE/UPSERT，控制帧不可丢；10秒校验与视图代际变化强制重同步。
- [ ] **测试背压。** 降低测试队列/字节上限，通过持续大消息让真实流发送阻塞，5秒后有明确错误或ctx退出；其他订阅者仍更新、存活内存有上限；取消后10秒内hub注册数量回基线。Pub/Sub不在此任务引入。
- [ ] **验收。** `go test ./app/entity/internal/subscription ./app/entity -tags=integration -run 'TestSubscribe|TestSlowSubscriber' -v -count=1`，A10/A11全过，替换Scaffold订阅断言。

## S07：持久网关、版本分配与离线补传（P0补齐/P1）

**依赖：** S03，端到端依赖S04。**验收：** A12、A13。**Files:** 新建 `internal/gateway/queue.go`、`generator.go`、`uploader.go`及测试，实现`cmd/gateway-simulator/main.go`。

**Interfaces:** C11 Queue API；另提供 `Generate(entityKey string, build func(version int64)(*commonv1.EntityStateEvent,error))(int64,error)`，同bbolt事务从entity_versions取下一版本，build只做纯转换，成功后事件/版本/序列同事务落盘；Enqueue用于已有事件重发，保存已提供的版本，不能擅自改身份。

- [ ] **先写失败用例。** 入库一条、关闭重开，越界ACK必须失败且pending仍有数据：

```go
if err := q.Ack(q.Epoch(), 99); err == nil { t.Fatal("accepted an ACK beyond sent data") }
pending, err := q.Pending(100)
if err != nil || len(pending) != 1 { t.Fatal("unconfirmed event lost after reopen") }
```

  Queue Ack只能验证已排队上界；uploader另记录实际sent上界并在调用Ack前验证。Run：`go test ./internal/gateway -run TestQueue -v -count=1`。
- [ ] **实现C06/O04。** 事务版本分配、持久epoch、水位安全删除、单批在途；正常/离线/只补传模式；产生与发送分开有界，磁盘满暂停生成并保留已有数据。offline模式无sent记录，不接受网络ACK。
- [ ] **进程测试。** `go test ./app/ingest -tags=integration -run TestGatewayRestart -v -count=1`：产生100条离线退出→同DB重启补传→pending0→最终版本正确；ACK送出后客户端落盘前强杀，允许重复但无丢失；压缩前后epoch/pending/版本计数相等。
- [ ] **验收。** A12/A13通过，报告积压量与净追平速率；不承诺积压期间新事件实时可见。

## S08：有限历史抽样、幂等与分页（P1）

**依赖：** S01、S04；六类输入依赖S05。**验收：** A14。**Files:** 新建 `app/entity/internal/history/sampler.go`、`app/entity/internal/repository/history.go`、`internal/pagination/token.go`和测试；实现ListHistorySamples。

**Interfaces:** `Sampler.Consider(event)(reason string, selected bool)`按C10维护每实体最后样本；`HistoryRepository.InsertOnce(ctx,event,reason) (inserted bool, err error)`同事务样本+去重key；`List(ctx,tenant,request)`带C03签名游标。读历史快照不重新按最新协议默默填未知字段。

- [ ] **先写失败用例。** 同一来源事件选中两次，实际样本只增1条；伪造/跨租户游标被拒绝：

```go
first, _ := repo.InsertOnce(ctx, event, "periodic")
again, err := repo.InsertOnce(ctx, event, "periodic")
if !first || again || err != nil { t.Fatal("sampling retry was not idempotent") }
```

  `go test ./app/entity -tags=integration -run TestHistory -v -count=1`；表内事件时刻用注入Clock，避免固定2026样例被年龄规则过滤。
- [ ] **实现C10。** 独立history group、白名单/总预算、单事件单reason、变化阈值、7天保留和老事件过滤；MySQL故障不提交历史消费位点，不影响projector group。不得每条状态先写MySQL再决定采样。
- [ ] **分页测试。** 相同occurred_at不同id跨页不重不漏，改filter/tenant/token签名失败，区间结束值排除；预算超限计数增加且内存保持有界，重启后可从已有样本恢复选择基线。
- [ ] **验收。** A14通过，保留原始events/s和实际samples/s；任何4%比例必须来自数据，不写固定性能成绩。
