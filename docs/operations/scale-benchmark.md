# 独立实体规模与写入压测

该入口驱动真实的机器来源鉴权、Ingest 流、Kafka 持久确认、Entity 投影、Redis 状态与 MySQL 历史链路。实体基数、每秒事件数和来源连接数分别计量。它不验证个人账号登录、任务执行、网关 bbolt 缓冲或订阅扇出；这些功能仍需完整回归。

本文说明运行方法与证据边界，不提供任何容量达标结论。命令中的目标规模只有在报告显示完整预热、全部负载档位完成且无失败时，才能作为对应配置的实测结果。

## 运行前提

从仓库根目录执行命令，安装 Docker Compose 和 Python 3。先准备包含当前 `verify`、`ingest`、`entity` 的后端镜像：

```powershell
docker build --target backend -t meshops-demo-backend:local .
```

依赖镜像为 `mysql:8.0`、`redis:7.2-alpine` 和 `apache/kafka:3.7.0`。如果本地缺少镜像，首次拉取也会消耗运行器的总时限；建议在正式计量前完成镜像准备。

运行器固定使用 `meshops-scale` Compose 项目，创建独立的内部网络和 MySQL、Kafka、Redis 卷，不发布主机端口。它不启动或停止 `meshops-demo` 项目。相同项目一次只允许一个运行器；本地锁位于 `.local/scale/run.lock`。

## 建议的分档顺序

先在较小基数上增加写速率，分辨生成器、确认与投影的瓶颈，再增加实体总数。以下命令分别运行，每次结束后先检查结果；失败后不要直接执行下一档。

```powershell
# 先检查一万实体下的写入速率。
python scripts/scale-benchmark.py --entities 10000 --rates 500,2000,5000 --seconds 30 --deadline-seconds 900 --warmup-seconds 600

# 一万实体检查完成后，再检查十万实体的初始化与状态存储。
python scripts/scale-benchmark.py --entities 100000 --rates 500 --seconds 30 --deadline-seconds 1800 --warmup-seconds 1200

# 只有此前证据与资源条件允许时，才执行百万不同实体的完整预热。
python scripts/scale-benchmark.py --entities 1000000 --rates 500,16667 --seconds 30 --deadline-seconds 10800 --warmup-seconds 7200
```

百万表示不同实体总数，不表示百万连接或百万事件每秒。来源始终为 10 个，人员、无人机、车辆、机器人各占实体总数的 20%，传感器和设施各占 10%。每个来源对应一条流、一个在途批次，当前批次为 1 条事件。注册表保留每个来源每秒 2000 条的原限额；超出限额导致的拒绝必须按实际失败解释。

这些时限是运行上限，不是初始化耗时预估。服务启动仍有现有的单服务 120 秒就绪限制；扩大外部总时限不会改变它。报告出现 `start_entity` 或 `start_ingest` 失败时，应先阅读对应服务日志和初始化阶段进度。

## 参数

| Python 运行器参数 | 默认值 | 范围与含义 |
| --- | --- | --- |
| `--entities` | `10000` | 10 至 1000000，且是 10 的倍数；不同实体总数 |
| `--rates` | `100,500` | 1 至 10 个不重复的整数，各为 1 至 100000；所有来源合计事件数/秒 |
| `--seconds` | `30` | 每个写入档位 5 至 300 秒 |
| `--deadline-seconds` | `900` | 10 至 10800 秒；含依赖启动的外部总时限 |
| `--warmup-seconds` | `600` | 1 至 7200 秒；包含预热发布、投影追平和全部快照检查 |
| `--sample-seconds` | `5` | 1 至 30 秒；外部容器资源采样间隔 |
| `--image` | `meshops-demo-backend:local` | 已构建的后端镜像；运行器不构建镜像 |

总时限到达后先停止供压，再执行有独立时限的取证和停止操作，因此命令返回可能晚于压测时限。超时、OOM、依赖失败、供压不足和报告遗漏都会返回非零退出码。

原本的本机入口继续保留默认的 10000 实体、100/500 条每秒语义：

```powershell
./scripts/benchmark.ps1 -Entities 10000 -Rates "100,500" -Seconds 30 -Timeout "15m" -WarmupTimeout "10m"
```

该入口使用 `scripts/env.ps1` 中配置的依赖，不提供独立 Compose 资源隔离。直接调用 Go 验证器时，对应参数为 `--entities`、`--rates`、`--seconds`、`--timeout`、`--warmup-timeout`；`--profile` 支持 `mixed` 和兼容的 `person` 对照组。

预热不受 `--rates` 限速：十个来源尽快各发布实体的第一条事件，随后最多用 30 秒排空消费，再逐一核验快照；完成后才进入指定速率。因此预热积压超时不等于后续 500/s 档位已测试失败。当前实测结果及失败边界见[八次容量记录](../../verification/2026-09-13-accounts-scale/capacity/README.md)。

## 固定资源边界

| 容器 | CPU 上限 | 内存上限 | 其他限制 |
| --- | --- | --- | --- |
| 验证器及其 Ingest、Entity 子进程 | 4 | 3 GiB | 同一个容器资源组；最多 512 个进程/线程 |
| MySQL | 2 | 1 GiB | InnoDB buffer pool 256 MiB；redo 磁盘容量 1 GiB |
| Kafka | 2 | 1.5 GiB | JVM 堆 512 至 768 MiB；3 个分区 |
| Redis | 2 | 1.5 GiB | `maxmemory 1gb`、`noeviction`、AOF |
| Kafka 卷初始化 | 0.5 | 64 MiB | 初始化完成后退出 |

依赖资源不包含在验证器的 3 GiB 限额内。容器限制属于上限，不代表宿主机预留了相同资源。需同时考虑演示项目、宿主机以及 Docker 虚拟机的可用资源。

## 如何判断结果

运行器最后输出 `outcome` 与 `summary.json` 路径。每次证据保存在 `.local/scale/runs/<时间-随机标识>/`：

| 文件 | 内容 |
| --- | --- |
| `summary.json` | 请求参数、固定资源、结果分类、退出码、超时标记、容器状态、停止结果和 Go 报告 |
| `resources.jsonl` | 按时序记录容器 CPU、内存、网络/磁盘计数及状态；运行时读取 cgroup OOM 计数 |
| `containers.log` | 有界保存专用容器日志与验证器阶段进度 |
| `.local/verification/<运行 ID>/benchmark.json` | 独立实体数、类型比例、预热计数、各档负载、延迟与失败阶段 |
| `.local/verification/<运行 ID>/diagnostics.jsonl` | 每 5 秒保存服务原始 Prometheus 指标、MySQL 全局状态计数、Redis 内存和运行统计 |
| `.local/verification/<运行 ID>/entity.log`、`ingest.log` | 被测真实服务的日志 |

`passed` 需要同时满足全部实体完成预热、全部档位完成、请求与实际数量一致、没有发布错误或生成队列丢弃、没有观测遗漏，投影和历史消费积压在最多 30 秒及剩余阶段预算内排空，并且实际生成速率及包含发布、观测和消费排空时间的吞吐均达到目标的 95%。资源或诊断采样失败也不能视作通过。该阈值是生成器与吞吐有效性检查，不是延迟服务等级；仍应单独审查 p99 和最大延迟。

失败分类包括 `under_offered`、`timeout`、`oom`、`failed`、`missing_report`、`incomplete`。原始失败原因和阶段比分类名称更具体。OOM 既查看 Docker 状态，也查看 cgroup `oom_kill` 计数，防止漏掉仅有子进程被杀死的情况。清理失败单独保留，不覆盖已知的 OOM 或超时原因。

核心字段的含义如下：

- `offeredEventsPerSecond` 是配置目标；`generatedEventsPerSecond` 由实际调度数量和生成耗时计算；`acceptedEventsPerSecond` 包含等待发布、观测和消费排空的时间，`consumerDrainSeconds` 单独记录双消费组排空耗时。三者不能互相替代。
- `requested`、`scheduled`、`accepted`、`generatorQueueDrops`、`errors` 分别区分计划数量、已安排事件、持久确认、生成队列丢弃和发布错误。
- `clientToKafkaAck` 计量一次流请求的发送至持久确认时间，不包含此前的生成队列等待。ACK 和消息大小分布采用最多 100000 个样本的无偏抽样。
- `generatedToObserved` 从事件生成开始，等待独立 RPC 查询首次读到同代且不低于该版本的快照，包含队列、处理、投影及轮询延迟；它不是精确的服务内部投影耗时。
- `observationCandidates`、`observationMisses` 和观测分布的 `samples` 共同检查观测完整性。稳态观测轮换覆盖来源；预热不采用抽样，每批至多 100 条，最后不足 100 条的尾批同样逐一检查身份、来源、类型、代次和版本。
- 源数据的 `stale_after` 保持 30 秒。状态存储保留过期快照；`warmupExpiredSnapshots` 单独计数已过期记录。完整预热证明这些实体的状态成功投影并可读取，不证明百万实体都仍处于新鲜状态。
- Go heap、容器内存与 Docker 虚拟机总内存属于不同指标。MySQL 状态计数多数为全局累计值，应比较相邻样本的差值；Kafka lag 需结合发布、投影及历史阶段解释。

初始化开始前即建立 Go 报告。正常返回的初始化失败写入具体错误；强制终止或 OOM 时应结合外部摘要、阶段日志和资源样本判断，不把尚未更新的报告当作完成。

## 保留数据与手动停止

运行器退出时停止自己的依赖并保留卷和证据。每次 Go 验证使用独立的数据库、主题前缀与状态命名空间，但专用卷会跨次保留历史数据。因此重复实验的初始磁盘、Redis 内存和数据库状态可能不同；比较结果时必须查看起始样本，不能默认依赖为空。

口令仅保存在已忽略的 `.local/scale/mysql-root-password`，报告记录其 SHA-256 摘要用于识别同一套本地依赖。Compose 环境文件只包含路径与运行参数。不要公开口令文件、生成的凭证环境或完整 Docker `inspect` 输出。

如果运行器异常中断且需要手动停止，使用该次生成的环境文件：

```powershell
docker compose --project-name meshops-scale --env-file .local/scale/runs/<运行目录>/compose.env -f compose.scale.yml stop --timeout 10
```

此命令保留卷。不要为了恢复压测而停止所有 Docker 容器或删除演示项目数据。若本地锁残留，先核对记录的进程已退出且专用项目没有正在运行的压测，再处理该锁；不要绕过正在生效的互斥检查。

## 工具自身验证

以下测试不启动 Docker 压测：

```powershell
go test ./internal/verification ./cmd/verify
go vet ./internal/verification ./cmd/verify
python scripts/scale-benchmark.test.py
```

注册表生成与加载的诊断基线也不访问数据库、Kafka 或 Redis：

```powershell
go test ./internal/verification -run '^$' -bench BenchmarkScaleManifest -benchtime=1x -benchmem
```

该微基准只覆盖 10000 和 100000 实体的 manifest 生成与真实注册表解析，报告的是每次操作时间与累计分配，不能替代容器 RSS 或百万实体全链路实测。
