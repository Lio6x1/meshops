# 运行配置、命令与输出规格

以下命令是 S/T/V 任务需要实现的接口，当前三个 cmd 仍是占位程序。本文不触发启动依赖、迁移或清空用户数据库。环境基线沿用 go.mod 和 docker-compose.yml，不引入新的服务种类。

## O01 配置与优先级

中心进程保留现有 `-f app/<service>/etc/meshops.<service>.v1.yaml`。S01将实际 config.Config 扩为 RpcServerConf + MeshOps 公共配置块；旧 configs/config.example.yaml 仍是设计参考，需要逐项接入，不复制它就假定生效。公共加载代码位于 internal/runtimeconfig，服务仅初始化自己需要的依赖。

| 公共配置键（MeshOps 下） | 类型/默认值 | 适用方 |
| --- | --- | --- |
| Manifest | string，必填 | 所有服务；路径相对 -f 文件目录 |
| KafkaBrokers | string数组，[localhost:9092] | Ingest、Entity、Task、Dispatcher |
| TopicPrefix/ConsumerGroupPrefix | string，空 | 独立测试资源前缀；只允许小写字母、数字、下划线、短横线 |
| RedisAddr | string，localhost:6379 | Entity |
| MySQLDSNEnv | string，MESHOPS_MYSQL_DSN | Entity、Task、Dispatcher；值是环境变量名 |
| RedisPasswordEnv | string，MESHOPS_REDIS_PASSWORD，可不存在 | Entity |
| EntityEndpoint/TaskEndpoint/DispatcherEndpoint | string，localhost:50052/50053/50054 | 按RPC调用方向使用 |
| ServiceTokenEnv | string，按服务必填 | 发起内部RPC的服务身份 |
| CursorKeyEnv | string，MESHOPS_CURSOR_KEY | Entity/Task，至少32随机字节；读实际密钥 |
| MetricsAddr | string，localhost:8080/8081/8082/8083 | 依服务；/metrics |
| UnaryTimeout | duration，5s | 所有Unary客户端 |
| ShutdownTimeout | duration，10s | 所有服务 |
| BatchSize/MaxMessageBytes | int，100/4194304 | Ingest/网关 |
| SubscriberQueueSize/SubscriberMaxBytes | int，1000/8388608 | Entity |
| ReconcileInterval/SlowConsumerTimeout | duration，10s/5s | Entity |
| OutboxPoll/OutboxLease | duration，500ms/30s | Task |
| AckTimeout/MaxAttempts/RetryDelays | duration/int/数组，30s/5/[5s,30s,300s,300s] | Dispatcher |
| HistoryPeriod/HistoryMaxEntities/HistoryBudget | duration/int/int，1s/100/200每秒 | Entity |

支持统一环境覆盖：MESHOPS_KAFKA_BROKERS（逗号分隔）、MESHOPS_REDIS_ADDR、MESHOPS_ENTITY_ENDPOINT、MESHOPS_TASK_ENDPOINT、MESHOPS_DISPATCHER_ENDPOINT。优先级为显式命令参数 > 环境覆盖 > YAML > 上表默认值；密钥类仅通过配置指向的环境变量读取。默认值只用于非敏感字段；缺少必需凭证、DSN、manifest、负数容量或零超时导致启动退出1，打印字段名不打印秘密。

YAML 示例（**待 S01 接入**）：

```yaml
Name: meshops.task.v1.rpc
ListenOn: 127.0.0.1:50053
Mode: dev
MeshOps:
  Manifest: ../../../configs/simulation.local.yaml
  MySQLDSNEnv: MESHOPS_MYSQL_DSN
  KafkaBrokers: [localhost:9092]
  EntityEndpoint: localhost:50052
  DispatcherEndpoint: localhost:50054
  ServiceTokenEnv: MESHOPS_TASK_TOKEN
  CursorKeyEnv: MESHOPS_CURSOR_KEY
  MetricsAddr: 127.0.0.1:8082
```

Go duration 使用字符串在配置加载时解析并验证；不要让不同服务以毫秒/秒两种单位解释同一配置。客户端流不套用5秒Unary总deadline；使用ctx取消、每批产生/等待ACK10秒上限和重连退避。首版只在localhost/隔离测试网络使用明文gRPC，禁止把测试身份解释为已部署的生产鉴权。

## O02 统一 manifest 与身份

保留 configs/simulation.example.yaml 的 sources、executors、task_catalog 字段，并在 S01/S05 增补以下完整结构。每个来源包含实体映射以及标准类型；每个实体定义自己的期望组件和可执行能力。示例路径只提供一条模板事件，生成时用 entity_id 和新版本/时间替换身份字段。

```yaml
tenant_id: demo_tenant
sources:
  - source_id: personnel_sim
    adapter: person
    source_generation: 1
    credential_env: MESHOPS_PERSON_SOURCE_TOKEN
    fixture: ../testdata/sources/person.json
    stale_after: 30s
    rate_limit_per_second: 100
    entities: {person-001: person-001}
executors:
  - executor_id: simulated_personnel
    entity_ids: [person-001]
    supported_tasks: [inspect]
    credential_env: MESHOPS_PERSON_EXECUTOR_TOKEN
actors:
  - id: demo_operator
    role: operator
    credential_env: MESHOPS_OPERATOR_TOKEN
  - id: demo_admin
    role: admin
    credential_env: MESHOPS_ADMIN_TOKEN
  - id: task_service
    role: task_service
    credential_env: MESHOPS_TASK_TOKEN
  - id: dispatcher_service
    role: dispatcher_service
    credential_env: MESHOPS_DISPATCHER_TOKEN
task_catalog:
  - task_type: inspect
    description: Simulated inspection
    parameter_schema_json: '{"type":"object","required":["duration_seconds"],"properties":{"duration_seconds":{"type":"integer","minimum":1,"maximum":60},"note":{"type":"string","maxLength":256}},"additionalProperties":false}'
```

actors、sources、executors 各自生成唯一 Principal；同一实际token不可分配给不同身份。token非空、至少32字节，比较用常量时间；manifest解析时拒绝重复source、同实体双来源、未注册执行实体和未知能力。例中省略其他类型仅为展示结构，最终配置须包含全部六类。

最终演示配置：来源 personnel_sim/drone_sim/vehicle_sim/robot_sim/sensor_sim/facility_sim，对应fixture和类型；每类一条时为6实体。扩展20实体时分布4/4/4/4/2/2，ID为类型名加三位数字。首四类分别绑定 simulated_personnel/simulated_aircraft/simulated_vehicle/simulated_robot，后两类无执行方。双租户测试再加载一个tenant_id=test_tenant的独立manifest，其来源ID用test_前缀（数据库source_id全局唯一），实体ID可相同。

manifest是本版停服修改的授权事实；seed工具把来源、实体、能力目录镜像写入MySQL。Entity/Task/Dispatcher启动时校验配置与数据库绑定一致，失败阻止readiness，不偷偷覆盖。Ingest仅校验manifest，不为此增加MySQL依赖；运维必须向四服务分发同一组manifest，Entity消费时仍按自己的权威绑定拒绝非法来源。001内历史demo_key不用于新演示，实际token仅从环境读取，来源表只保存SHA256指纹；调试日志不能输出metadata。

gRPC metadata固定 `authorization: Bearer <token>`。不接受请求体tenant_id替代认证身份。每个服务支持多个manifest路径（MeshOps.Manifest可用逗号分隔的路径字符串）用于双租户测试，路径相对服务配置文件；每个模拟器只选择一个租户。

| 身份 | 允许RPC |
| --- | --- |
| source | Ingest.ReportEntityStates，仅自己的source/实体 |
| operator | Entity全部查询/订阅，Task创建/取消/查询，Dispatcher.GetDispatch |
| executor | Executor.ListenTasks；Task.ReportTaskStatus与GetTask，仅绑定executor及实体的任务 |
| task_service | Entity.GetSnapshot/BatchGetSnapshots；Dispatcher.GetDispatch，同租户内部查询 |
| dispatcher_service | Task.GetTask、ReportTaskStatus，仅DISPATCHED迁移；Dispatcher的worker使用对应身份 |
| admin | 上述操作员查询与Dispatcher.RetryDLQ/GetStatus；本地seed/rebuild管理入口 |

内部调用必须显式使用该租户service principal；不把外部executor token转发给Entity或Dispatcher。角色校验在RPC边界，资源绑定校验在业务层，不能只隐藏CLI选项。

## O03 启动与初始化

1. 检查 git status，确认实际完成任务；业务未实现时相应命令必须返回明确错误。
2. 在新测试数据卷上启动 kafka/redis/mysql，等待健康检查。已有数据库按迁移版本升级，禁止用 down -v 解决迁移冲突。003和后续迁移须先通过空库与002升级测试。
3. 运行 `opctl seed --manifest configs/simulation.local.yaml --dsn-env MESHOPS_MYSQL_DSN --token-env MESHOPS_ADMIN_TOKEN`。同内容重复seed不改变绑定；现有行不同则失败并列出字段差异，不自动覆盖。seed只接收本地文件，不是公网注册接口。
4. 创建Kafka三个topic：entity-state-events.v1、task-events.v1、task-dlq.v1，均3分区、复制因子1，开发保留配置沿用Compose。由seed的Kafka初始化步骤幂等创建，已有分区数不符时失败，禁止自动增分区打乱实体映射。
5. 启动Entity、Task、Dispatcher、Ingest四个服务；RPC连接懒建立，readiness只检测直接存储依赖和manifest/schema，不递归调用其他服务的readiness导致循环等待。命令日志明确启动地址和schema版本，不输出DSN。
6. 先启动网关，让数据产生，再启动执行方和操作端。执行方未连入时任务可以持久pending，不能凭“stream不存在”丢弃任务。

SIGTERM/interrupt流程：readiness=false→停止接收新流/新任务→取消worker context→在10秒内等待在途事务/producer回调→关闭clients→退出。未完成Kafka处理不提交位点；本地队列未ACK不删。liveness表示进程循环存活，readiness表示完成初始化且存储可用；两者在metrics HTTP同端口 `/livez`、`/readyz`，无需另建网关。

## O04 CLI 确定接口

公共输出：stdout一条JSON对象/一条流帧每行，采用protojson的lowerCamelCase字段；stderr仅诊断。普通成功退出0、运行失败1、用法错误2；当前占位退出2保留到实现替换。grpc错误stdout为 `{"error":{"code":"FAILED_PRECONDITION","message":"..."}}`，进程退出1。`--token-env`只接受环境变量名，命令行不直接传token；opctl默认MESHOPS_OPERATOR_TOKEN，seed/retry-dlq/status须显式给admin变量名，模拟器可取manifest中所选source/executor的credential_env。

| 命令 | 参数与行为 |
| --- | --- |
| `opctl snapshot` | --entity ID、--endpoint（默认Entity）、--token-env；一次GetSnapshot |
| `opctl subscribe` | --entities逗号ID、--duration默认30s、--token-env；自动全量同步重连，打印SNAPSHOT_END，退出前打印当前视图实体数 |
| `opctl history` | --entity、--start/--end RFC3339、--page-size、--page-token；打印一页结果 |
| `opctl task create` | --entity、--type inspect、--duration-seconds、--note可选、--key必填、--deadline可选；类型/参数不通过任意JSON注入 |
| `opctl task get` / `opctl task history` | --id TASK_ID；查询实际状态、最终resultJSON或审计 |
| `opctl task list` | --entity可选、--status可选、--page-size、--page-token |
| `opctl task cancel` | --id、--reason必填；打印success、cancelRequested和currentStatus，不改写成“已停止” |
| `opctl dispatch get` | --task、--dispatch可选；打印投递和业务状态 |
| `opctl dispatch retry-dlq` | --task、--reason必填、admin token；调用RetryDLQ |
| `opctl dispatcher status` | admin token；GetStatus统计当前租户 |
| `gateway-simulator` | --manifest、--source、--db必填；--rate默认20、--batch默认100、--endpoint默认Ingest、--token-env、--duration默认60s |
| `gateway-simulator --offline` | 只生成并持久化，duration到期退出0；同DB在线重启会先补传 |
| `gateway-simulator --drain-only` | 不产生新事件，补传已有队列，清零退出0，--timeout默认60s，超时退出1保留DB |
| `gateway-simulator --compact` | 不连接网络，离线校验、压缩与保留备份 |
| `executor-simulator` | --manifest、--executor、--db必填；--endpoint默认Dispatcher、--task-endpoint默认Task、--token-env、--concurrency默认4 |
| `executor-simulator --mode fail/reject/normal` | 默认normal；测试固定失败或接受前拒绝；不修改公开业务payload |

模拟器允许 `--duplicate-every N`（0关闭；每N条重发同一原始事件）用于验证去重，重复只增加上传sequence，不增加entity_version；若该能力会破坏Queue.Generate原子版本分配，应先生成原记录后复制其完整内容入队。`--seed`默认1让实体选择、位置变化和电量变化可复现，实际时间使用可注入Clock；测试使用固定Clock。

网关退出/周期统计JSON字段：generated、sent、confirmedSequence、pending、reconnects、epoch；不要把sent当作确认条数。执行方统计accepted、completed、duplicateCommands、effectCount；其中effectCount从持久结果聚合。opctl订阅依据版本维护最终视图，过期只标stale，不把传感器数值0当缺失。

## O05 配置变更需要同步的文件

S01接入各 app/internal/config、internal/svc、各服务main及 app/*/etc；S05扩展simulation.example；V01补三个cmd和Makefile帮助。README仅列已经实现且验证过的命令；本规格中的未来命令在完成前保留本页说明，不搬成“快速开始已可运行”。

每个敏感环境变量都应在 `.env.example` 中只列空值和用途；真正运行用系统环境或ignored的simulation.local.yaml/config.local.yaml，不能把token写进fixtures。版本、异常场景和验收证据见[acceptance.md](acceptance.md)。
