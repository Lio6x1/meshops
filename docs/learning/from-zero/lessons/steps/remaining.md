# Z05、Z06、Z08、Z09：持久链路到最终验收

继续使用 `D:\job\golang\projects\meshops-course-lab`。本页承接 [前面的小步骤](README.md)，每步先读解释，再打开对应文件清单复制完整文件。清单里的go build检查整个当前工程，指定测试必须逐项PASS，不能以SKIP或“没有测试”替代。

手工路线不运行apply-checkpoint；未在清单出现的已有文件保留，新增/修改文件整份复制，二进制`.bin`文件直接复制原文件。不要把所有后续阶段先复制进去，否则无法判断本步依赖是否齐全。

## 真实依赖接线

Z05-01、Z06-01—03都是本地测试，不需要Docker。Z05-02与Z06-04需要真实Redis和Kafka，先复制完本步包含的compose，再在学习目录执行：

```powershell
docker compose up -d --wait --wait-timeout 180
if ($LASTEXITCODE -ne 0) { throw 'dependencies not healthy' }
$env:MESHOPS_TEST_REDIS_ADDR = '127.0.0.1:26379'
$env:MESHOPS_TEST_KAFKA_BROKERS = 'localhost:29092'
```

`MESHOPS_TEST_`变量专门给测试使用，不是启动服务所需的全部配置。复制文件不代表容器已启动；端口拒绝连接时，先看 `docker compose ps -a` 和对应服务日志，不去修改投影函数。

从Z06进入Z07按已有说明停止旧服务与旧compose。Z07以后使用完整课程环境，Redis16379、Kafka19092、MySQL13306。完成Z07初始化后，Z08-02的测试接线是：

```powershell
. ./scripts/env.ps1
$env:MESHOPS_TEST_MYSQL_ADMIN_DSN = $env:MESHOPS_MYSQL_DSN
$env:MESHOPS_TEST_REDIS_ADDR = $env:MESHOPS_REDIS_ADDR
$env:MESHOPS_TEST_KAFKA_BROKERS = $env:MESHOPS_KAFKA_BROKERS
```

这里必须设置ADMIN_DSN：已有state测试TestMain用它创建随机命名的独立数据库、执行迁移，再把测试DSN指向该库；结束只删除这个生成的测试库。不要直接把MESHOPS_TEST_MYSQL_DSN指向需要保留的业务库运行历史清理测试，它会执行保留策略删除。

如果完整环境尚未准备，先按Z07执行 `./scripts/initialize.ps1`。若另一学习目录已占用默认环境，先按 [环境说明](../../CHECKPOINTS.md)处理，不能用新凭证强行接管旧绑定。验证教师当前采用16379/19092做独立key与topic测试，学生Z05/Z06仍按各自compose的26379/29092连接。

## Z05-01：消息客户端先学会超时退出

前置是Z04完成，先停止其服务。打开 [Z05-01文件清单](z05-01.md)，这次只增加bus包四个文件，旧内存查询实现还保留。

按New → Publish → Consume → consumePartition阅读。New创建客户端配置，不等于已连上Kafka；Publish等待写入确认；Consume负责分配分区；consumePartition负责按分区串行处理及提交。lag和retention文件提供位点诊断，被客户端引用，因此也一起复制。

先看context这段代码（解释片段，完整文件在清单）：

```go
ctx, cancel := context.WithTimeout(ctx, operationTimeout)
defer cancel()
return k.writer.WriteMessages(ctx, kafka.Message{Topic: topic, Key: []byte(key), Value: append([]byte(nil), value...)})
```

子context沿用父context取消信号，再加本次操作上限；defer在成功和失败时都会释放计时资源。`append([]byte(nil), value...)`复制消息内容，避免调用者后来修改原切片影响发送。返回error给Ingest，它才能决定是否确认网关队列。

本步测试在本机随机端口开一个只接受TCP连接、永不回复协议的端点，分别检查Ping、EnsureTopics和Lag能按调用方deadline返回。它不是一个正常Kafka实例：证明的是“对方不响应时不会永远卡住”，不证明消息已经持久化。

成功应显示TestOperationsHonorContextWithSilentBroker及其子测试PASS。如果操作成功返回nil，反而违反这个实验的预期。下一步才使用真实broker。

## Z05-02：把内存实现换成Redis投影

打开 [Z05-02文件清单](z05-02.md)，移除MemoryEntity和它的测试，加入Entity、指标、真实依赖测试及两个进程的配置/入口。调用者与构造函数一起替换，不能只新增entity.go却仍把MemoryEntity传给Ingest。

完成依赖接线后，运行文件页指定的两个测试：

1. TestRedisAtomicOrderingAndTombstone依次写版本2、重复版本2、版本1、同版本冲突、DELETE版本3、旧版本2。检查返回值1/0/-1/-2及最终删除状态，删除标记没有物理TTL；重新创建Entity对象后仍读到版本3。
2. TestKafkaACKThenRedisProjection先通过真实Kafka收到ACK，此时还没启动消费者，查询应为空；随后启动消费者，最终才能读到版本1。这个顺序证明ACK与可见状态是两个时间点。

测试使用独立generation和topic，不清空Redis，也不修改别的视图active指针。测试通过后再按 [Z05正文](../z05.md)的多终端命令启动Ingest、Entity和客户端，观察真实进程之间的调用。

注意：Lua脚本把“比较版本”和“写入”放在同一次执行里，Go层先读后写无法提供相同的跨进程原子性。旧版本应忽略，不应该一直重试到把消息队列堵死；同版本不同内容要暴露冲突，不能悄悄覆盖。

## Z06-01：先验证四种新格式，暂不启动新网关

停止Z05程序，打开 [Z06-01文件清单](z06-01.md)。新增车辆、机器人、传感器、设施fixture，替换Normalize与测试。此时simulation配置还没升级，先在函数测试中验证转换，不尝试从CLI启动新来源。

TestSixAdapters逐个加载六种fixture。重点自己算两组数：车辆36km/h变成10m/s；机器人energy转换成百分比。再确认人员没有Power，设施没有被塞进车辆载荷字段。

TestDroneCoordinatesAndFacilityCapacityAreValidated检查无人机整数坐标和设施占用槽位约束。未知、null、明确的零是不同输入；例如电量0有效，缺少必填电量不能默认成0。数组集合先规范排序去重，避免相同技能只是顺序不同却被识别成内容冲突。

两项测试PASS只说明适配函数符合约定，网关配置与消息持久链路要到Z06-04完整接上。

## Z06-02：一笔事务保存版本与待发事件

打开 [Z06-02文件清单](z06-02.md)，只加queue.go和queue_test.go。先理解三个编号：entity version排序同一实体状态，queue sequence排序本地待发记录，epoch标记队列身份；它们不能互相代替。

读Generate时圈出bbolt Update闭包。版本分配、构造事件、容量检查和入队在同一写事务里；闭包返回error则一起回滚。容量满时不应先消耗版本然后丢掉事件。

按清单运行五项测试，逐项对照意义：

- 重开文件后保留队列与确认状态；ACK不得超过实际发送位置。
- 容量不足或生成失败回滚，不能留下半条记录。
- 重复事件不再分配一个新版本。
- 文件损坏时报错并保留原文件，不能自动新建空库假装恢复。
- 压缩后保留原文件和必要状态，压缩不是删除历史依据。

这些测试使用真实临时bbolt文件，不依赖网络。读测试里的t.TempDir：每个测试拥有自己的临时目录，不会改你的data网关队列。

## Z06-03：先发送，再处理连续确认

打开 [Z06-03文件清单](z06-03.md)，新增uplink和测试。Upload负责连接失败后的退避与重连，uploadStream负责一条流中的批次及ACK。它们通过Queue取得稳定事件，不因重发重新生成event ID。

六项测试分别验证：ACK丢失及关闭发送侧后的最终ACK、只重发未确认后缀、非法ACK不删数据、长流不套普通短RPC总时限、取消解除阻塞、退避有上限。测试使用gRPC测试端点，不是远程Kafka；目的在于把网络时序固定下来。

想象sequence101—103已经发送，ACK只到101。下一轮只需102—103；如果102其实已经写入但ACK丢了，重发也必须保留相同内容和身份。这里的可靠性来自“本地不提前删＋远端能幂等”，不是连接永不掉线。

## Z06-04：最后组装订阅和六类模拟网关

打开 [Z06-04文件清单](z06-04.md)，加入simulation、GatewayCLI、入口、Subscribe及新配置。Entity与subscription相互引用，作为一个完整编译步骤同时加入；没有用空Subscribe先占位。

指定Kafka/Redis测试确认本次装配没有破坏持久上报链路，但该测试不直接验证订阅。因此还必须按 [Z06正文](../z06.md)执行六来源离线生成、同库drain、查询及watch命令。不要把这项ACK测试PASS写成“订阅所有边界已经验证”。

watch先收初始化数据和SNAPSHOT_END，再接收变化。pending按实体合并最新版本，控制帧不进入这张map。wake容量1用于通知“有变化可取”，真正的数据保存在pending中；通知已存在时default返回，不会丢掉唯一数据副本。

本阶段course watch是15秒观察工具，结束会报告deadline；Z07完整opctl处理正常时长退出。慢消费者和重连初始化的更强回归测试在完整参考实现中已有，最终集成验收仍要覆盖它们。

## Z08-01：构造函数增加数据库，采样规则先单测

前置是Z07全部完成。停止服务，打开 [Z08-01文件清单](z08-01.md)。history.go、Entity构造、app装配与采样测试一起更新。原三参数构造改为接收数据库的版本，调用者必须同步，不能只改函数定义。

按sampleReason → lastSample → Sample阅读。判断顺序是首次样本、状态变化、位置变化、速度/航向变化、时间周期；同一事件只保存一个命中原因，不因满足多个条件插入多行。lastSample从数据库恢复采样基线，重启后不假装没有采过样。

TestSamplingPriorityAndCircularHeading不需要数据库，验证规则优先级以及航向角绕过0度时的最小夹角。例如359到1度应看作2度，不是358度。成功只能说明采样判断正确，还没证明MySQL写入、去重和分页。

此时旧opctl仍不开放history命令，下一步加入客户端与真实数据库验收。

## Z08-02：查询分页与真实历史清理

打开 [Z08-02文件清单](z08-02.md)，替换opctl、增加历史和保留测试。按上文设置ADMIN_DSN，让TestMain建立独立数据库，再运行两项指定测试。

TestMySQLSampleIdempotencePagingAgeAndBudget验证实际数据库中的重复采样、分页、过旧事件及预算边界。TestHistoryRetentionCatchesUpMoreThanOneBatch插入超过一批的过期记录，再检查清理能追平，而不只是完成第一批就留下积压。

分页按occurred_at与id两个字段排序。时间相同的多条记录不能只用时间翻页；游标还绑定租户、过滤条件、首次采样时间上界与有效期。拿同一个token换实体/时间范围应拒绝，不是从任意位置继续查。

测试结束后，按 [Z08正文](../z08.md)启动模拟器并查询最近10分钟样本。第一次为空时等待采样并刷新end时间，不要无限复用一个早于样本的结束时刻。历史是样本，不保证每次上报都有一行。

## Z09-01：校验新视图，不先改active指针

打开 [Z09-01文件清单](z09-01.md)，加入rebuild与对应测试，先用Redis独立generation执行TestRedisOrderingTombstoneAndManifest。它验证版本顺序、墓碑与manifest期望；期望版本/操作错误时必须失败，不会在本步骤测试中切换业务active指针。

按loadExpected → VerifyGeneration → Rebuild阅读。expected manifest来自独立输入依据，不能用刚重放出的结果给自己出标准答案。Kafka保留窗口可能已经丢失某实体最后一条事件，即使读取到末尾也不等于完整恢复。

完整Rebuild还要固定分区起止边界、重放到影子空间、确认预期、按旧active值做条件切换。本步骤源码已经有这些函数，但运行入口的维护flags到Z09-03才接入；不在这里手改Redis active指针试验。

## Z09-02：验证器自身也要处理失败

打开 [Z09-02文件清单](z09-02.md)，加入verification包与verify入口。四个指定测试不停止容器、不跑整轮压测：它们使用可控回调验证恢复控制流、错误合并、压测排空预算和报告写入失败。

TestStopUncertaintyStillRestoresDependency模拟“停止操作可能已发生，但返回错误”，仍须尝试恢复。TestRestoreErrorsAreNotLost要求同时保留原始故障与恢复失败，不能让finally覆盖最初原因。duration预算测试确认计时负载之后还有排空时间；报告写入失败测试要求非零退出，不能只在内存里算出成功就返回0。

这些是验收工具的测试，不是系统已经通过故障恢复和性能测试的替代证据。

## Z09-03：补齐维护入口，再执行最终验收

打开 [Z09-03文件清单](z09-03.md)，最后替换main/app、build脚本，加入faults/benchmark脚本和已有参考报告。已有报告是对照资料，不是你本机本轮执行的结果，不能复制后直接拿来宣称自己的实验通过。

本步整个工程编译后，八个程序都已齐全。先完成 [排错实验](../debugging.md)，再按 [Z09正文](../z09.md)运行常规、集成、进程、故障与性能验收。影子恢复按维护流程暂停写入、准备独立manifest，不宣传为持续写入下的无损在线热切换。

压测报告分别读ACK延迟和抽样可见延迟；100/500 events/s是提供的负载档位，不是测出的系统极限。订阅扇出、任务执行和网关磁盘吞吐也不是这一个数字就能代表的。

所有步骤结束后，源码应与最终reference逐文件一致。接下来重点是从学生操作路径复核命令与理解难点，而不是继续扩大任务类型、实体种类或中间件清单。
