# Z04—Z10：按小步骤实际搭建

这组步骤用于亲手搭建路线。每页列出本次必须复制的完整文件和精确测试命令，完成后整个当前工程都能 `go build ./...`，不需要预先放入整个阶段答案。先读本页对应步骤的解释，再打开文件清单；主讲义 [Z04](../z04.md)、[Z07](../z07.md) 提供更完整的业务推演。

全程使用 `D:\job\golang\projects\meshops-course-lab`。Z01—Z03按原讲义完成，之后依次执行下列23个步骤；不要把后续文件提前复制。每步结束版本由“上一步文件＋本页精确变化”确定，完整原文件均已提供。

| 阶段 | 顺序与文件清单 | 配套操作解释 |
| --- | --- | --- |
| Z04 | [01](z04-01.md) → [02](z04-02.md) → [03](z04-03.md) → [04](z04-04.md) | 本页 |
| Z05 | [01](z05-01.md) → [02](z05-02.md) | [依赖与持久投影](remaining.md) |
| Z06 | [01](z06-01.md) → [02](z06-02.md) → [03](z06-03.md) → [04](z06-04.md) | [六类来源、队列与订阅](remaining.md) |
| Z07 | [01](z07-01.md) → [02](z07-02.md) → [03](z07-03.md) → [04](z07-04.md) → [05](z07-05.md) | 本页 |
| Z08 | [01](z08-01.md) → [02](z08-02.md) | [历史与分页](remaining.md) |
| Z09 | [01](z09-01.md) → [02](z09-02.md) → [03](z09-03.md) | [恢复与验收](remaining.md) |
| Z10 | [01](z10-01.md) → [02](z10-02.md) → [03](z10-03.md) | [任务搜索逐步讲解](../z10.md) |

## 与阶段复制脚本怎么配合

手工路线按这些清单操作，不运行apply-checkpoint。受管理路线可以继续一次应用整个阶段再读这些步骤；若想中途改为亲手搭建，先把当前已完成阶段的源码复制到新的学习目录，排除 `.course`、`.local`、bin、data等管理记录和运行产物，后续按手工路线持续推进。新目录进入Z07时需要自己的初始化环境，不能用新凭证直接接管另一目录的既有数据库。

不要在受管理目录里一边手改文件、一边调用apply-checkpoint升级；它会因为源码hash不一致拒绝，这属于保护学生修改。无需删除阶段记录强行绕过。默认从Z01就选择手工路线最容易理解。

## Z04-01：先让新的协议能编译

前置：Z03写入、查询与重启丢失实验完成，旧服务已经停止。打开 [本步文件清单](z04-01.md)。这一页的移除项是旧临时Put接口的调用者及旧教学测试；可以备份到工程外，不要把备份的 `.go` 文件留在原包内，否则Go仍会编译它。

按清单先复制锁定依赖，再写公共事件、任务目录、Entity与Ingest协议，最后复制生成文件或运行本步的 `scripts/generate.ps1`。本阶段协议中有未来任务目录定义，是因为EntitySnapshot引用它，不表示已经新增Task服务。

生成过程可选，但命令不能猜：

```powershell
Set-Location 'D:\job\golang\projects\meshops-course-lab'
$env:Path = 'D:\go1.25.10\bin;D:\my_projects\bin;D:\protoc-36.0-rc-1-win64\protoc-36.0-rc-1-win64\bin;' + $env:Path
$env:GOWORK = 'off'
./scripts/generate.ps1
go build ./...
if ($LASTEXITCODE -ne 0) { throw 'generated packages cannot build' }
go test ./gen/...
```

`--go_out=gen`指定输出根，`paths=source_relative`按Proto相对路径生成到 `gen/common/v1` 等目录；go_package仍是import约定，不是Windows磁盘路径。插件位置和版本沿用Z01。

成功输出只有生成包的 `[no test files]`，这是正确的编译检查。本步骤处于接口升级中，暂时没有服务入口；到Z04-04恢复查询和上报。不能运行旧Put命令，也不必添加一个空实现只为了让旧命令存在。

完成后你应能指出：EntityStateEvent是一次上报，EntitySnapshot是状态内容，entity_version与队列sequence不是同一个编号。

## Z04-02：先把原始JSON转换正确

打开 [本步文件清单](z04-02.md)。创建platform的类型、配置、registry，再创建adapters、validation、两个fixture及适配测试。此时还没有Ingest，验证的是转换函数；config.go包含后续会复用的数据库辅助函数，但本步不调用它，不需要启动MySQL。

用人员测试逐行理解函数输入：

```go
src := platform.Source{
    TenantID: "t", ID: "s", Adapter: "person", Generation: 1,
    StaleAfter: 30 * time.Second,
    Entities: map[string]string{"p": "p"},
}
```

这是解释片段，完整测试在本步文件表内。

1. `platform.Source{...}`创建配置结构体；其中的身份来自可信配置，不来自JSON自行声明。
2. `map[string]string{"p":"p"}`将上游人员编号p映射为平台编号p。两者这次相同，但仍经过映射检查；未经登记的编号必须失败。
3. `30*time.Second`是时间长度，用于从发生时间计算有效期；不是睡眠30秒。
4. Normalize接收原始字节、这份配置和接收时间，返回事件指针与error。返回error时不继续使用事件。

本步两个测试必须都PASS。TestStrictPerson用原始JSON变体分别检查小数版本、缺少必填字段、负版本、只有一个坐标、多余JSON对象；正常人员数据不携带Power组件。测试使用固定输入，不需要连接设备，也没有把数据库mock成功当成持久化验证。

遇到 `unregistered raw entity ID`，检查fixture里的employee_id与Entities映射；遇到找不到fixture，确认从学习工程根目录运行 `go test ./internal/state ...`，不要随意移动testdata目录。Go包测试的工作目录是包目录，所以测试使用 `../../testdata/...`。

## Z04-03：把“能转换”接到“允许谁发送、确认到哪里”

打开 [本步文件清单](z04-03.md)。这次只新增auth、ingest和接入规则测试；不启动网络进程。platform.Identity读取context中的Principal，Ingest再比较请求内的身份与它是否一致。

测试中的 `recordPublisher{fail:2}` 是故障注入替身：第2次Publish返回错误。它使“1成功、2失败、3成功”的时序可重复，不代表真的连接了Kafka。主循环应调用Publish三次，但ACK只能到1；测试同时核对错误项对应sequence=2。

再看同一个测试的第二半：将第二个事件的EntityVersion改成-1，预期在整批预校验阶段拒绝，Publish调用次数为0。这两段的区别正是“格式不合格”和“外部写入途中失败”。

`TestResumeAndEpoch`检查流内epoch与连续位置。`TestSingleSourceStreamAndPersistentRateBudget`先占用来源槽位再模拟第二条流，确认被拒绝；取消第一条流后槽位释放。随后耗尽限流预算再重连，不能通过新连接获得一份全新预算。

此处active与限流表是本进程内状态，不是跨多台Ingest的分布式协调。课程当前部署边界下成立，不应把它说成任意水平扩容后仍有全局单连接保证。

运行文件页上的三个精确测试，出现 `[no tests to run]` 先检查是否漏了 `ingest_test.go` 或拼错名称。此时还没有数据查询，这是下一步的工作。

## Z04-04：注册服务并跑完整RPC

打开 [本步文件清单](z04-04.md)，加入MemoryEntity、RPC测试、course入口和环境脚本。先运行该页的构建与两个测试。

`TestAuthenticatedWriteQueryAndVolatileRestart`使用bufconn：真实gRPC客户端、序列化、拦截器、服务注册与流方法均参与，但传输在进程内内存连接上，不占TCP端口。它依次验证无凭证拒绝、来源不能当操作员查询、上报ACK、操作员查询、非法ID、不存在实体、重新创建内存仓库后查不到旧数据。不是一次真实进程崩溃实验。

然后按 [Z04正文的双终端命令](../z04.md)启动真正的course进程并查询。读server.go时依次定位下面四个对象：

```go
memory := state.NewMemoryEntity(r)
ingest := state.NewIngest(cfg.MeshOps, r, memory)
register = func(s *grpc.Server) {
    entityv1.RegisterEntityServiceServer(s, memory)
    ingestv1.RegisterIngestServiceServer(s, ingest)
}
```

这是解释片段，完整server.go在本步清单中。r是可信来源目录；memory保存当前事件并提供查询；它同时实现Publisher，所以传给Ingest。注册函数把生成协议里的服务名接到这两个实际对象上，不会自动创造业务实现。

最后由zrpc.NewServer使用RpcServerConf监听配置地址，启动代码处理信号和结束等待。客户端main负责命令参数、构造请求、打印结果；服务端注册函数不会自行调用客户端。

这一步结束后源码与完整Z04阶段逐文件相同。按正文用personnel_sim和drone_sim上报，两个查询都应该found=true；Ctrl+C停止服务。下一步回到Z05，不要再次复制Z04整阶段覆盖刚完成的练习。

## Z07-01：增加任务协议和纯规则，先不接数据库

前置：已经完成Z06。先停止网关和两个服务，保留队列文件。打开 [本步文件清单](z07-01.md)。已有状态链路源码保留，这次添加三个任务方向的协议及生成包、domain.go、第一版domain_test.go。

本步测试文件只含参数校验、幂等hash、状态转移三个已有测试的完整实现。下一步会整份替换成包含游标与迁移测试的最终版本。不是让你删除测试直到代码能编译，而是按依赖出现的时间引入对应职责。

输入例子：inspect的 `{"duration_seconds":1}`有效；0、61、1.5、重复字段和null无效。正常执行必须先ACKED再EXECUTING；成功后再上报EXECUTING应被终态屏障拒绝。

这里的 `CanTransition` 是普通Go函数，可以在无数据库时确定规则是否正确。但它不能自己防止两个进程同时改任务，下一步把规则放入数据库事务才处理并发事实。

本步go build编译整个当前工程，三个精确测试都应PASS。尚没有Task main，不能发送create命令；这是规则完成，不是任务已能创建。

## Z07-02：一次引入数据库事实与Outbox，分两遍读

打开 [本步文件清单](z07-02.md)。加入Service、store、queries、migrations、workers与SQL，替换完整domain_test.go。

为什么这次一起出现？Service调用persistChange写Outbox，worker使用同一个Service保存的数据库与配置；把这些相互依赖的完整函数拆成几个有空函数的文件只会造成另一套临时实现。因此代码一次复制齐全，阅读分两遍：先CreateTask与store，再Run与publishOne，详见 [Z07正文](../z07.md)。

先把数据库事务中的步骤标出来：INSERT任务 → writeAudit → persistChange（状态及Outbox）→ Commit。`defer tx.Rollback()`保证中途返回会回滚，成功Commit后它不会撤销已提交结果。看到任何 `tx.ExecContext` 都问一次它属于哪个事务；不能随意替成s.db执行。

第二遍沿Outbox看：领取租约的事务先提交，之后才等待Kafka；成功标记用lease_owner条件；失败保留原event ID。由此推出一种必须接受的情况：Kafka成功但published标记失败，会再次发送同一事件。

本步测试能证明纯规则、游标校验和SQL脚本拆分；没有运行真实MySQL事务，也没有运行Kafka发布。本步仍不启动服务。数据库错误、唯一约束与事务边界的真实测试集中在Z07-05装配后运行，不能仅凭TestHistoricalMigrationsRemainExecutable的名字声称迁移已经在MySQL执行过。

## Z07-03：把任务事件变成可追踪的分发尝试

打开 [本步文件清单](z07-03.md)。interfaces.go中的 `var _ Interface = (*Type)(nil)`是编译期接口检查：声明的空白变量不保存业务值，转换的nil指针不创建对象；编译器检查Type是否实现Interface全部方法。

阅读dispatchOne时，先分清Task事实和dispatch尝试。任务可能已经执行，某次传输记录却还没有等到ACK；不能因为一次传输超时就再次创建新任务。execution key跨重试不变，dispatch ID随尝试变化。

本步精确测试构造有界内存发送队列，依次断言：同命令重复排队不占新槽位、字节上限拒绝、数量上限拒绝、实体绑定不匹配拒绝、其他租户不能选中此流。该测试不启动执行方，也不验证真实MySQL lease竞争。

读 `send` 中的非阻塞select与错误返回：慢客户端不能让任意业务worker永久卡在写channel，也不能被无限队列吞掉。具体队列上限与字节计数要同时保留，只有个数限制时，大消息仍可能吃掉过多内存。

## Z07-04：真正写一个本地inbox文件

打开 [本步文件清单](z07-04.md)。加入inbox、执行循环及五个inbox测试；它们使用临时目录里的真实bbolt文件，不依赖Docker。

重点按顺序阅读 `TestInboxEffectAndReportSurviveRestart`：打开文件 → 接收命令 → 提交结果 → 保存待报内容 → 关闭 → 重新打开同一个文件 → 再接收相同命令 → 再提交相同结果 → 断言effect count=1并且待报内容完全相等。

`fresh`和`changed`都是bool返回值：首次为true说明首次接收/提交；重复时为false且error=nil表示已处理，不是失败。判断时必须同时看bool与error，不能将false一概当作需要重新执行。

取消先落盘的测试在重开文件后再次送来执行命令，应该保留取消标记而不产生效果。竞争测试让取消和结果事务竞争，只允许符合事务顺序的结果。损坏测试要求报错并保留数据，不允许“打开失败就创建一个新的空inbox”。

五个测试都必须PASS。这里证明本地事务及重开文件行为，不等同于真实无人机物理动作只执行一次。executor的网络回报恢复在装配步骤还有独立测试。

## Z07-05：最后换入口、配置与环境

打开 [本步文件清单](z07-05.md)。这一步文件较多，因为加入所有启动入口、脚本和真实集成测试；它们不再混入前面规则学习阶段。先停止Z06程序并在旧配置仍存在时运行 `docker compose stop`，随后才替换compose和配置。

按用途阅读剩余文件，而非按文件名字母顺序：

1. configs与platform registry决定地址、可信来源/执行方和凭证读取位置；env负责在本机生成或加载环境，不把token写进笔记。
2. internal/app负责组装依赖和服务生命周期；四个服务main把命令交给它，避免每个入口重复创建一套不同连接逻辑。
3. internal/cli与opctl入口实现用户命令；gateway/executor CLI把参数交给已学习的队列和执行循环。
4. initialize/build/start/stop负责运行顺序；testdata、tests和协议基线是验证输入，不是业务服务。`.bin`基线直接复制，不能另存为文本。

先执行本步go build和两项executor测试，它们使用测试RPC端点验证串行回报身份与结果恢复；然后进入真实依赖：

```powershell
Set-Location 'D:\job\golang\projects\meshops-course-lab'
./scripts/initialize.ps1
./scripts/test.ps1 -Integration
try {
    ./scripts/start.ps1 -Simulators
    ./scripts/demo.ps1
} finally {
    ./scripts/stop.ps1
}
```

initialize会准备依赖、schema和可信绑定。若已有另一目录使用默认完整课程数据库，先按 [运行手册](../../CHECKPOINTS.md)的环境说明处理；凭证不匹配不靠删库解决。

集成测试带integration构建标签，默认go test不会自动运行它们；脚本负责设置所需依赖。demo应观察到六类状态、四类inspect和effect_count=1。真依赖的事务、Outbox与取消/超时测试通过后，才能说这些跨进程行为已验证。

至此文件与原完整Z07逐项一致。继续Z08加入历史，再到Z09恢复/性能验收；不需要重新设计任务类型、把优先级扩大成调度算法或加入Agent。

## 统一排错顺序

先确认当前目录与 `go env GOMOD`，再核对本步文件清单，最后看本步测试名。`undefined`通常是文件漏复制或复制了后一步测试；接口错误常见于Proto变更后仍使用旧gen；测试未发现先查名称和测试文件，不删除断言。

某步只有包测试时，不提前尝试尚不存在的main；某步有真实运行命令时，不以单测PASS替代。每小步只新增明确的保证，前面的已验证业务源码没有被临时空实现替换。
