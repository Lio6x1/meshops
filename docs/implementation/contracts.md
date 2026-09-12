# 交付与协议规格

本文保留当前核心业务契约，MUST 表示必须满足的性质；功能与验证状态见 [当前入口](README.md)。S/T/V 是历史实施任务编号，不是当前未完成清单。六类字段样例在 `testdata/sources/`，确定性转换结果见 `testdata/contracts/normalized-expectations.json`。

## C01 身份、类型与来源

- 实体主键为 (tenant_id, entity_id)，权威绑定为 (source_id, source_generation)。六类 entity_type 固定 person/drone/vehicle/robot/sensor/facility；注册后的类型不可通过遥测改变。
- tenant_id/source_id 最长 64 字节，其余业务 ID 最长 128；幂等请求键最长 256；均只接受 `[a-z0-9_-]+`。Kafka 状态 Key 固定 tenant_id + `:` + entity_id。task_id 由服务端生成小写 UUID，execution_key=task_id。
- entity_version 与 source_generation 范围 1..9007199254740991，schema_version=1。同实体版本跨网关重启持续递增；新 epoch 不重置实体版本。task.status_version 为从 1 开始的 int32，每次变更加一。
- 来源和执行方凭证分离；凭证派生租户、角色、来源/执行方身份，不能信任消息字段。注册绑定初版只在停服状态由 seed 工具维护；运行中不提供热切换来源或执行方的 API。
- 第一版所有状态事件是完整 UPSERT 或 DELETE。UPSERT 要有 snapshot 和已注册的类型；DELETE 无 snapshot，仍携带实体版本、来源和事件 ID。DELETE 的源身份与版本校验不放宽。
- 每个实体可绑定一个 executor_id，任务能力取注册配置与 TaskCatalog 的交集。遥测不能新增可执行任务或改变绑定。person/drone/vehicle/robot 的演示绑定支持 inspect；sensor/facility 无执行方且目录为空。

## C02 六类组件与字段补充

保留已存在的字段号。在 `proto/common/v1/entity.proto` 的 EntitySnapshot 已包含 vehicle=11、robot=12、facility=13，消息结构如下：

```proto
message VehicleState {
  optional double load_kg = 1;
  string availability = 2;
}
message RobotState {
  repeated string fault_codes = 1;
  string availability = 2;
}
message FacilityState {
  string facility_type = 1;
  bool is_open = 2;
  uint32 occupied_slots = 3;
  uint32 total_slots = 4;
}
```

Location.altitude/accuracy 与 Velocity.heading/vertical_speed 已使用 proto3 optional，字段号不变；新接入禁止通过标量零值猜测“未知”。经纬度在原始数据中两者同时必填；来源若缺失定位，整个 location 消息省略，不能只省纬度或经度。现有 PersonState.on_duty 在原始人员数据中必填。

| 类型 | 必有组件 | 可选组件 | 本版原始格式/映射 |
| --- | --- | --- | --- |
| person | person | location | employee_id→ID，version→版本，observed_at→时间；latitude/longitude→度；on_duty、availability、skills 原样验证 |
| drone | power | location | aircraft_id→ID，revision→版本，timestamp_ms→毫秒时间；lat_e7/lon_e7÷10^7，altitude_cm÷100；battery_pct→百分比，flight_state→status |
| vehicle | vehicle、velocity | location、power | vehicle_id，sequence，recorded_at；position.lat/lng→度，speed_kmh÷3.6→m/s，load_kg→载荷，availability→status；可选battery_pct→power.battery_percent，未提供则无power |
| robot | robot、power | location | robot_id，revision，timestamp；pose.latitude/longitude→度，energy.ratio×100→百分比；fault_codes、availability→机器人组件 |
| sensor | sensor | location | sensor_id，version，measured_at；measurement.value/unit→读数/单位，health→健康，site.lat/lon→度，status 固定 online |
| facility | facility | location | facility_id，revision，observed_at；coordinates.latitude/longitude→度；kind→设施类型，open、slots.occupied/total→占用；status=open/closed |

person.status=availability。drone 的合法状态为 idle/flying/returning/fault；person/vehicle/robot 的 availability=idle/busy/offline。sensor.health=ok/warn/fault，温度示例 unit=celsius；facility_type 本版只用 charging_station。禁止非有限数值，电量范围 0..100，能源比率 0..1，载荷和速度非负，occupied_slots≤total_slots。纬度[-90,90]、经度[-180,180]，WGS84；不实现坐标系自动转换。skills 与 fault_codes 去重并排序；人员离岗不自动等于无位置。

每类 adapter 只解码规定字段，未知顶层字段报错；JSON 数字先按目标整数/浮点范围校验，禁止把 1.5 版本截成 1。原始 ID 经 Binding.EntityIDs 映射，未注册返回错误。原始事件 ID 字段：人员 observation_id、无人机 sample_id，其余 event_id。事件重试保留原 ID；来源不同可有相同原始 ID，中心去重身份包含来源。

已有无人机样例的supported_tasks是允许接收的字符串数组，但只作来源自报信息，adapter不据此生成capability或TaskCatalog；执行能力统一由中心绑定生成，不能信任来源扩权。其余未列出的顶层字段仍拒绝。

统一 occurred_at 为 UTC Timestamp。原始遥测不接受客户端传入 received_at；Adapter.Input.ReceivedAt 用于可控测试，实际由 Ingest 接收时覆盖。expires_at=occurred_at+配置中的 stale_after（默认30秒）；这表示观测时效，不阻止离线旧事件进入 Kafka。新模拟事件发生时间取当前 UTC，静态样例时间只用于确定性转换测试。

## C03 鉴权与所有 RPC 的结果

身份传递、配置字段及角色白名单见 [operations.md](operations.md)。每个 Unary 和 Stream 都必须经过鉴权。对不属于租户的实体/任务查询表现为不存在，避免泄露其他租户对象；有身份但调用了禁止的 RPC 返回 PERMISSION_DENIED。

| 情况 | gRPC 结果 |
| --- | --- |
| 缺失/无效凭证 | UNAUTHENTICATED |
| 来源无权上报该实体、执行方绑定不符 | PERMISSION_DENIED |
| 非法值、缺字段、批次/范围越界 | INVALID_ARGUMENT |
| 暂未实现的过滤或任务类型 | UNIMPLEMENTED |
| 请求键重复且规范化参数不同 | ALREADY_EXISTS |
| 数据库/Kafka/Redis 无法完成必要操作 | UNAVAILABLE；不能返回空成功 |
| 限流、缓冲队列满、连接名额耗尽 | RESOURCE_EXHAUSTED |
| 版本并发冲突 | ABORTED |
| 实体过期、不具备能力、非法任务迁移、代际/队列恢复基线冲突 | FAILED_PRECONDITION |
| GetTask/GetDispatch 指定对象不存在 | NOT_FOUND |

GetSnapshot 不存在/已删除返回 found=false，不附带其他租户信息；存在但过期返回 found=true、原状态与 expires_at，由客户端标记 stale。Redis 未就绪不能伪装成 found=false。BatchGetSnapshots 要求1..100个唯一ID，按输入顺序返回每个结果；任一依赖错误使整次RPC失败。Subscribe 禁止空集合与未支持过滤，首次全量同步中的不存在项用缺席表示。

GetTask/ListTasks/GetTaskHistory 根据认证租户过滤。ListTasks.page_size 默认20、范围1..100，status=UNSPECIFIED 表示不过滤；顺序为(created_at DESC, task_id DESC)，游标绑定租户、过滤条件、最后一项和首屏时间上界，total_count 为该时间上界内当前满足条件的计数，任务状态并发变动时不承诺多页原子视图。GetTaskHistory 按 status_version 升序；第一版单任务上限1000条（正常状态转换远低于此），超过返回 RESOURCE_EXHAUSTED 并在新增业务前扩展分页。

单次 ListTasks 响应中的 COUNT 与本页 SELECT 使用同一只读 REPEATABLE READ 事务，保证本次总数与本页来自同一读快照。关闭 rows 后提交；下一次翻页会建立新的事务快照，不能把单页一致性解释成跨页冻结。

游标编码为 base64url(JSON payload + HMAC-SHA256)，验证长度≤2048字节、签名、类型、租户、条件指纹及位置，再查询；过期时间15分钟，失败 INVALID_ARGUMENT。签名密钥见运行规格。JSON payload 字段为 v=1、kind、tenant_id、filter_hash、last_time、last_id、upper_time、expires_at。历史查询使用(occurred_at ASC, id ASC)，范围半开且≤24小时，page_size默认100/最大500；禁止 OFFSET 深分页。

## C04 上传与连续 ACK

每条流绑定一个 source，单实例 Ingest 对活动 source 只允许一个流，冲突返回 RESOURCE_EXHAUSTED；流结束释放名额。客户端持久 queue epoch，first_sequence 为批首且从1开始；每批1..100条，sequence 连续，整条消息≤4MiB。首批必须 first_sequence=resume_after_sequence+1，后续批必须紧接当前 ACK；resume_after_sequence 在本流中保持不变。

服务端不会持久保存网关 ACK。首批 resume 值是**客户端持久记录的起始基线**，中心不验证其历史真实性，也不据此删除数据；协议保证成立的前提是客户端仅持久化真实服务端 ACK，测试必须验证这一点。要防恶意客户端丢弃自身数据需要额外持久上传会话，本版不做也不宣称。

```text
整批校验，校验失败整批拒绝且不向 Kafka 发送
按输入顺序 Produce，收集每条最终结果
从当前水位起扫描成功前缀，遇失败即停止推进
成功：回复同 epoch 的新连续水位
部分失败：回复已确认前缀和 EventError(sequence)，随后 UNAVAILABLE 关闭流
客户端：同 epoch / 单调 / 不超已发送上界的 ACK 落盘，再删队列前缀
重连：从最后已落盘 ACK+1 重放，保留事件内容和身份
```

received_at 不参与重复内容哈希，其余领域内容用确定性 protobuf 编码后 SHA256；重复抵达可以有新的 received_at。投影处理同 generation/version/event_id/hash 为重复；同版本不同身份/内容为冲突，记指标和隔离日志并跳过，不能覆盖较新快照。生产成功只保证 Kafka 配置的 ACK，不表示投影已更新。

## C05 投影、订阅与恢复算法

一个 Entity 进程负责 projector 与订阅索引。Kafka 使用显式提交，按分区串行处理；不同分区有界并发，不允许同分区后记录先提交越过未处理记录。持久依赖失败停住该分区，处理成功或已明确拒绝并记录原因才提交。非法编码消息输出带 topic/partition/offset 的结构化隔离日志和计数，再提交；日志不输出凭证或完整不可信 payload。

Redis Key 为 `view:{generation}:tenant:{tenant}:entity:{id}:snapshot`；一个 Hash 包含 source_id、source_generation、version、event_id、payload_hash、snapshot（二进制）、updated_at、expires_at、deleted。默认控制键 `meshops:view:active` 存激活视图 UUID；隔离环境使用非空 TopicPrefix 时，控制键同时带此前缀，避免不同事件流共用恢复证据。Lua 原子判断来源代际和版本并更新；权威元数据仍来自注册绑定。租户/实体编号先经过字符集校验才能拼接。

Hash 不设置物理 TTL；expires_at 是逻辑过期，DELETE 留墓碑；核心版不自动清理版本记录。Redis noeviction，OOM 作为可观测错误处理。清理/长期归档是后续管理功能。新来源代际切换仅在停服并更新配置、重建视图后进行，本版无热切换写入竞态。

订阅算法：先在租户索引登记请求ID集合和 bounded pending-map，再读每实体快照并发 SNAPSHOT；接着发 SNAPSHOT_END，再发送缓冲中的较新版本。控制帧不可合并；同实体 UPSERT/DELETE 可按版本保留最新。单连接最多100实体、队列1000条且总编码字节≤8MiB；缓冲满或连续5秒无法发送则 RESOURCE_EXHAUSTED，注销索引并退出发送协程。ctx取消必须打断发送，10秒内清理所有相关goroutine。每10秒 HEARTBEAT，含sync_id/view_generation，不代表补发保障。

**原子更新 Redis 与进程内通知间仍有崩溃/取消窗口。** 重复投影不能因为“Redis已是同版本”就完全不通知：重处理时可重发当前版本，客户端去重。订阅每10秒重新核对所订阅实体版本；缺口或视图代际变化结束当前流并返回 FAILED_PRECONDITION，客户端全量重连。初始化期间核对失败也重来，不把扫描快照称作跨实体原子快照。

周期核对由同一 Entity 实例共享：按租户/实体去重后批量读取版本元数据，再逐连接比较已发送、已知和待发送版本。广播先过滤关注的实体再克隆消息。首个订阅注册时启动巡检，最后一个注销时取消并在索引锁外等待退出；共享读取不改变每条流独立的队列、版本和错误边界。

恢复工具是 Entity 二进制 `--rebuild-view --generation <uuid> --expected-manifest <path>` 模式，不新增服务。演示先暂停生成、等网关补传清零、停止普通Entity，再捕获Kafka各分区起始/结束位点；独立reader重建影子命名空间到该结束水位，校验输入清单中的每个最新版本及DELETE后原子切换active，然后退出重建模式并重启普通Entity。expected-manifest是测试输入产生的JSON数组，每项含tenant_id/entity_id/source_generation/entity_version/operation；它不是从Redis导出的期望值。

影子恢复不提交旧 projector group 位点，也不清理旧命名空间。普通 Entity 的消费组包含当前视图 UUID；同代次重启续传，丢失视图后创建新代次则重新回放。初始代次严格从零检查完整性；验证重建完成后，将 verified 标记与 active 指针原子激活，再以每个分区已验证的独占 End 作为恢复下界。提交位点比下界新时保留其进度；Kafka 保留起点超过应读位置时明确失败，不能自动接受不完整后缀。恢复证据还绑定原事件主题，不能借用其他环境的边界。停止普通 Entity 避免在线切换期间遗漏数据；这仍是有维护窗口的恢复，不是在线无损切换。抽样历史使用独立固定消费组，允许有日志诊断的有损保留期恢复，不享有最新视图恢复下界。

本版恢复验收限于受控数据集，Kafka 包含所有实体的最新UPSERT/DELETE。冷实体事件超保留时输出明确失败/缺失列表；不通过 MySQL 历史抽样冒充完整最新事实。正常业务只启动一个 Entity，动态多实例仍为可选扩展。

## C06 网关本地队列

bbolt buckets：meta(epoch,next_sequence,confirmed_sequence)、pending(8字节大端sequence→Protobuf事件)、entity_versions(tenant/entity→最近分配版本)。创建新模拟事件时，在同事务中推进实体版本与队列序列并写入payload；持久失败不分配“已成功生成”的计数。删除已确认事件和更新 confirmed_sequence 也在同一事务。epoch 仅在创建全新空库时生成。

重启校验 epoch 非空、序列无空洞；pending非空时最小值=confirmed+1且next=最大值+1，pending为空时next=confirmed+1。不成立则退出1并保留原文件；禁止自动重建队列掩盖损坏。容量为最多100000条或pending编码字节1GiB，先到者生效；磁盘满返回RESOURCE_EXHAUSTED等效CLI错误并暂停产生新事件。bbolt文件实际大小另设磁盘保护告警，已删页面不计成“未确认事件字节”。

主循环按序发送一个在途批次；重连退避1..30秒、±20%抖动；SDK内部有限重试≤3次，应用层只在流失败后重连，不再给单条事件开无限并发重试。恢复发送上限默认500events/s，新产生默认20events/s/来源。发送低于生成速率无法追平，必须显示净消化速率；暂停生成和暂停网络分别支持确定性测试。

压缩是网关 `--compact` 离线模式：确认无运行进程持有该DB，读取原库→bbolt.Compact到同目录新文件→校验水位和数量→关闭句柄→保留备份并替换。失败原库可恢复，要求至少一个库文件大小的额外空间，不保证暂停100ms。

## C07 inspect 与任务事实

输入只允许 duration_seconds（整数1..60）、note（可选，UTF-8≤256字节）；未知参数拒绝。priority=0归一为5，仅支持5。deadline省略时取首次创建时间+5分钟；显式值必须晚于服务器当前时间且不超过24小时。规范化请求哈希包含类型、执行实体、归一priority、归一payload、以及**原始deadline是否提供和其值**，不包含自动产生的deadline/created_at，避免无deadline请求重试产生冲突。

同幂等键重试先查原任务并比较哈希，再考虑当前实体是否过期，保证首次创建后实体离线仍能返回原成功。首次创建须读取Entity快照及注册执行能力；ID格式错误INVALID_ARGUMENT，合法ID但不存在NOT_FOUND，过期、离线、busy/fault、人员离岗、未声明inspect均FAILED_PRECONDITION。这只是创建时检查，不是资源预留，任务执行前还要由执行方确认；不实现跨任务实体资源锁。

一次事务：插入CREATED/version0任务→写CREATED审计(version0)→推进DISPATCH_PENDING/version1并写审计→插入CREATED类型TaskEvent(current_status=DISPATCH_PENDING,status_version=1)的Outbox→提交。两条审计共享创建请求关联信息，但event_id分别唯一。CreateTask返回已提交的pending状态；`tasks`已包含`result JSON NULL`和`completed_at TIMESTAMP(6) NULL`，common.Task新增result_json=19、completed_at=20，使GetTask能查询最终模拟结果。

TaskEvent.data_json 统一为完整 Task 的 protojson，Kafka value 为整个TaskEvent的protobuf；Outbox.payload为TaskEvent的protojson。所有变更都发事件，使用 tenant_id:task_id 做Kafka Key。Dispatcher只对CREATED/CANCEL_REQUESTED产生新命令，其余用于更新本地投递状态，避免成功事件再次触发执行。

首次进入任一终态时completed_at取服务端提交时间。SUCCEEDED保存C08结果；其他终态result为空，失败详情在审计reason中。迟到回报只保留审计，不覆盖result或completed_at。

状态迁移沿用框架导航；每次修改 tasks/version、history、execution_reports（若有）、outbox 同事务。请求回报event_id+hash已存在时先返回DUPLICATE及当前任务状态，不再检查旧expected版本；新回报先验证权限/绑定/dispatch，再检查终态迟到规则，最后版本及合法迁移。ABORTED 不记录“已接受”的回报，重新读取后需要改变内容时生成新的回报event_id。

## C08 分发、执行与回报的缺口补充

Task服务验证回报所引用的dispatch。GetDispatchRequest已包含dispatch_id=2，空表示最新attempt；GetDispatchResponse包含command_id=9、execution_key=10、delivery_status=11（pending/dispatched/acked/executing/succeeded/failed/timeout/cancelled/abandoned/dlq）、command_kind=12（execute/cancel）。现有TaskStatus status字段仅表示关联任务最近已知状态，不能将其当作投递记录状态。Task通过只读内部RPC核对租户、task_id、executor_id、execution_key及命令种类，不直接写Dispatcher表。

新回报还必须引用具有 `dispatched_at` 的尝试。该时间表示发送前已持久化投递意图；只有 pending 行、从未建立投递意图不能推进任务状态。它不证明设备已经物理收到命令，也不排斥实际收到命令的历史 timeout/dlq 尝试回报。分发器在远程查询 Task 后重新锁定尝试并核对任务镜像版本，防止旧查询覆盖新终态；人工 execute 重试还须拒绝已 ACKED/EXECUTING 的任务或更新镜像，取消命令仍按取消语义处理。

初始EXECUTE命令ID=`<task_id>-execute`，取消ID=`<task_id>-cancel`，dispatchID=`<command_id>-<attempt>`，attempt从1开始。同命令再次发送当前attempt内容不变；确认超时后的新尝试才增加attempt。每task/command_kind/attempt数据库唯一。持久待发队列落库后才提交Kafka位点；命令先到而Task状态尚未推进时允许合法ACK，不允许状态回退。

Dispatcher接到CREATED事件时，先GetTask核对尚未终态/取消，再事务插入确定的命令记录；检查后发生取消仍依靠执行方取消墓碑与任务状态机收敛。每个executor只允许一条ListenTasks流，队列上限100、字节上限8MiB；同命令去重。没有连接保持pending，按next_attempt_at重试；发送和等待ACK不能持有数据库事务锁。

对已有投递记录按last_task_status_version条件更新任务状态镜像，旧事件不得回退镜像、复活旧attempt或创建新的命令；相同版本按重复处理。尚无记录的旧CREATED仍先查当前Task，只有任务仍可投递时才创建初始记录。任务进入终态后停止该任务所有命令重试，待发取消命令也关闭；历史timeout/dlq事实及其时间保留。GetDispatch的delivery_status描述指定attempt，status描述最近已知任务状态，两者可不同。

执行方bbolt buckets：inbox(execution_key→task摘要、首次dispatch、accepted/running/terminal、cancel标记)、reports(event_id→待回报序列)、results(execution_key→确定结果及effect_count)。接收落盘后生成ACK回报，再串行回报EXECUTING；运行的计时可在重启后重新等待，唯一模拟副作用是终态事务中effect_count从0写为1并保存结果，不把“计时重启”计为又执行一次业务。

`inbox_pending` 与 `inbox_active` 是派生索引，与主记录在同一 bbolt 事务维护；运行期间只扫描待处理键，并最多读取容量上限所需的 active 键。打开旧文件时先完整校验事实，再原子重建索引；Done 与待确认报告同时存在、保留的 EXECUTE 载荷非法等情况拒绝打开并保留文件。完成墓碑与结果不自动删除，因此这是热路径扫描优化，不是无期限磁盘保留策略。

成功结果JSON固定为 `{"inspection_id":"<task_id>","entity_id":"<id>","outcome":"ok","effect_count":1}`。不生成随机成功内容；故障测试可通过模拟器参数选择fail/reject，但不能把测试开关放进公开任务payload。重复执行命令读取已有inbox/result，允许重发待确认回报，不产生第二份结果。执行方忙时并发上限4，超过容量的任务在未接受时回报REJECTED，不无界排队。

ReportTaskStatus的发生时间首次产生时固定；重试整条回报保留身份和哈希。状态回报单任务串行，先提交ACK再EXECUTING再结果，读GetTask获取最新expected_status_version。Dispatcher更新DISPATCHED和取消请求可能制造冲突：重新读取后若合法迁移仍成立，重新生成回报事件；若终态则发送迟到结果用于审计。一个executor可执行多个已绑定实体，凭证的entity_ids必须覆盖目标。dispatcher_service调用ReportTaskStatus时executor_id表示已分配执行方，必须等于task和dispatch绑定；该内部角色只允许DISPATCHED，不能套用“executor_id等于服务凭证ID”或冒充执行方报告结果。

## C09 取消、超时、Outbox 与 DLQ

- CancelTask首次请求设置cancel_requested、写reason、version+1、自迁移审计和CANCEL_REQUESTED事件；重复取消不再推进版本或改写首次reason。已CANCELLED返回success=true，已SUCCEEDED/FAILED/REJECTED/TIMED_OUT返回success=false及现状；未知NOT_FOUND。是否已停止由current_status判断。
- 执行方收到CANCEL先将墓碑与待回报落盘，随后在同一execution_key事务下和结果提交竞争；未开始的EXECUTE随后到达也不得执行。CANCELLED回报必须引用cancel类型dispatch；SUCCEEDED/FAILED必须引用execute类型dispatch。
- Task超时worker每1秒扫描deadline到期非终态，条件更新TIMED_OUT并发Outbox；执行超时默认5分钟，由创建时deadline限制，ACKED后不自动续期。迟到回报事务写task_execution_reports，disposition=late_result，任务终态/result保持原值。
- Outbox单实例worker每500ms抢占最多100条，lease30秒，网络发送deadline5秒；按task串行，较小id未发布时不能抢占该task较大id。发送成功条件标记published；失败记录next_attempt_at（1秒起，封顶30秒）和错误，始终保留未发布记录。重启扫描到期租约，版本去重允许重复发布。
- 分发ACK超时30秒，最多5次自动尝试；等待时间5s、30s、300s、300s（从上一尝试失败起算），每次单独持久化记录。未连接也计为一次到期失败。业务REJECTED/FAILED不自动重新执行业务；只有传输未确认可重投。
- 达到尝试上限将该命令投递记录置dlq；任务保持原非终态直到deadline或合法回报，避免把“未收到ACK”误认为执行失败。DLQ **MySQL为事实**，新增task_dispatches字段 dlq_at/dlq_published_at（均TIMESTAMP(6) NULL）、last_task_status（INT NOT NULL DEFAULT 0，对应Proto枚举）、last_task_status_version（INT NOT NULL DEFAULT 0）和status枚举dlq；Kafka task-dlq.v1只做通知，成功后标记，可重复，不作为唯一恢复来源。
- RetryDLQ仅admin可调用：按task锁定最新dlq记录，仅任务未终态且未过deadline可受理；继续相同command_id/execution_key，attempt递增，并允许一轮最多5次重试。为区分轮次，新增retry_round INT NOT NULL DEFAULT0；同一dlq记录重复请求只创建一条下一轮首attempt，其他返回accepted=false及原因。旧attempt永不改成新业务。超过deadline只能查询审计，重新业务执行由新CreateTask触发。

GetStatus按认证租户统计：active_tasks为最新已知非终态且有未关闭命令的去重task数；dlq计数只含各命令最新attempt仍为dlq且任务未终态的记录，历史旧轮次不重复计入。该查询是Dispatcher本地最终一致视图，不能代替Task事实查询。

## C10 历史抽样与迁移补充

保留原001/002，增量迁移 `migrations/003_implementation_contracts.sql` 承载C07/C09列、任务状态审计唯一约束(tenant_id,task_id,status_version)、历史幂等表。升级前检查旧审计版本重复，发现不一致失败并列出冲突，不静默删除；新唯一约束不要求event_id全局唯一。

历史采样group独立消费，按实体在内存记录最后样本。启动/重平衡后从MySQL读取该实体最新样本，允许选择边界因重启不同，但相同事件不能落两份。触发规则按优先级选择一个reason：status变化→state_change，位置变化≥50米→position，速度变化≥20%或方向≥30度→velocity，距上样本occurred_at≥1秒→periodic；第一条为periodic。速度从0到正值视为变化，方向用圆周最小夹角。模板里的task_event采样延后，核心任务审计由Task负责。

本版源数据超过7天直接跳过历史写入并计数（状态链路独立）；历史保留7天，按小批DELETE维护已有p_future分区，不自动DDL扩分区。历史实体白名单默认最多100个，periodic最多100条/秒；变化样本进入总预算200条/秒，超预算丢弃该次**抽样候选**并计数，不声称保存完整轨迹，不影响Kafka最新状态投影。

新增 `history_sample_keys`：tenant_id VARCHAR64、source_id VARCHAR64、source_generation BIGINT、event_id VARCHAR128、sampled_at DATETIME(6)、sample_id BIGINT，主键为前四列。插入样本与该去重键同一MySQL事务，sample_id对应entity_history_samples.id；同键重复忽略且不再新增样本。去重键只存被选中的样本，和样本同步保留7天；旧事件重放不得绕过事件年龄过滤而复活已过期历史。需要完整历史的需求必须重新评审容量，本版不承诺。

所有MySQL会话使用UTC（DSN parseTime=true、loc=UTC、连接初始化 SET time_zone='+00:00'），不依赖Compose宿主时区。返回时间统一RFC3339 UTC；写JSON使用protojson或明确的任务结构，不能混用Go生成字段名和Proto JSON字段名。

## C11 验证与局部实现接口

本节导航到实际实现，不再列不存在的占位包和计划函数签名：

| 规则或机制 | 当前源码 |
| --- | --- |
| inspect 参数、优先级与状态转换 | [internal/tasks/domain.go](../../internal/tasks/domain.go) |
| 可信身份与实体来源绑定 | [internal/platform/registry.go](../../internal/platform/registry.go)、[auth.go](../../internal/platform/auth.go) |
| 六类原始格式转换 | [internal/state/adapters.go](../../internal/state/adapters.go) |
| bbolt 队列、版本生成与连续 ACK | [internal/edge/queue.go](../../internal/edge/queue.go) |
| 执行接收、待处理索引与持久结果 | [internal/edge/inbox.go](../../internal/edge/inbox.go) |

接口签名以源码为准，完整可复制实现以对应课程的文件答案为准；实现拆分不能删减外部契约与验收断言。业务行为与日期证据从 [验收矩阵](acceptance.md) 进入，不以本文说明代替测试结果。

读取normalized-expectations时，common与每case.fields合并，sourceId/entityId取case.source_id/entity_id；比较Go消息语义或使用protojson EmitUnpopulated=true。absent表示消息/optional字段无presence，序列化后省略或null都接受；空repeated字段与空数组等价，不能把默认JSON省略行为当转换失败。该文件只定义预期，不由当前Adapter生成。
