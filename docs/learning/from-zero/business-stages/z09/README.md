# MeshOps 多源实体实时协同与可靠任务调度平台

本目录是固定的 Z09 教学答案；仓库根目录继续开发搜索扩展。此阶段的 Go module：`example.com/meshops-course`。它使用模拟数据来源和模拟执行方，复现六类实体的统一接入、状态查询/订阅、inspect 任务下发与执行跟踪。旧骨架已移除，业务程序、协议、配置和脚本都在当前根目录。

**开始学习：[Z00—Z09 课程目录](../../../../../docs/learning/from-zero/lessons/README.md)。** 教材与阶段答案位于 `docs/learning/from-zero/`，你自己的学习工程仍使用 `D:\job\golang\projects\meshops-course-lab`。

原定关键实现及 A01—A28 已有 [验收记录](../../../../../verification/2026-09-10/summary.md)。课程有完整文件答案和连续复制证据，全部讲解与操作仍需最终统一复核。**任务搜索（MySQL → Canal → Kafka → ES）及对应课程尚未实现。** 最新维护状态见 [工作记录](../../../../../docs/learning/from-zero/BUILD-LEDGER.md)。

## 数据经过哪些地方

```mermaid
flowchart LR
  A[六类模拟来源：原始 JSON] --> A1[网关适配 / bbolt 待发送队列]
  A1 -->|统一 Protobuf 事件、序号| B[Ingest：身份、来源与事件校验]
  B -->|等待 Kafka ACK| C[(Kafka 状态事件)]
  C --> D[Entity：版本校验与投影]
  D --> E[(Redis 当前视图)]
  D --> F[查询与订阅]
  C --> G[历史抽样]
  G --> H[(MySQL)]
  I[opctl 指定执行实体] --> J[Task：事务、状态机、Outbox]
  J --> H
  J --> K[(Kafka 任务事件)]
  K --> L[Dispatcher：投递、重试、DLQ]
  L --> M[模拟执行方 / bbolt inbox]
  M -->|状态回报| J
```

人员、无人机、地面车辆、巡检机器人可以执行同一种 `inspect`；固定传感器和设施仅提供状态。每个实体绑定一个权威来源，任务执行方由注册信息确定，操作员选择目标实体。本项目不包含路径规划、最优资源分配或真实设备控制。

## 本机前提

使用 PowerShell，以下命令的工作目录都为本文件所在目录。先确认工具在 PATH 中：

```powershell
go version
docker version
docker compose version
```

已使用 Go 1.25.10 构建；依赖版本以 [go.mod](go.mod) 和 [go.sum](go.sum) 为准。已提交 `gen/`，直接构建不要求先安装代码生成工具。修改 Proto 时才需要 `protoc`、`protoc-gen-go` 和 `protoc-gen-go-grpc`，并执行 [generate.ps1](scripts/generate.ps1)。生成代码不能手改。

在当前用户电脑上，若 PATH 未包含已安装的 Go，可在当前终端执行：

```powershell
$env:Path = 'D:\go1.25.10\bin;' + $env:Path
Set-Location 'D:\job\golang\projects\meshops'
```

其他电脑按实际 Go 安装路径设置；上述盘符不是 Go module 的一部分。`example.com` 是课程模块名中的占位域名，引用本模块文件时 Go 在本地寻找，运行本项目不用申请这个域名。

## 初始化、启动、实际演示

先启动 Docker Desktop 的 Linux engine，再执行：

```powershell
./scripts/initialize.ps1
./scripts/start.ps1 -Simulators
./scripts/demo.ps1
```

初始化脚本启动专用 Compose 服务、构建八个程序（包含验收工具 `verify`）、执行 001—004 增量迁移并导入来源/实体绑定。数据源与执行方凭证第一次运行时生成在 `.local/secrets.json`；再次运行会复用，避免已有队列突然失去身份。不要把这个文件提交到仓库。

启动脚本运行四个服务、六个来源模拟器和四个执行方。来源默认每秒上报两次、运行 30 分钟；过期后重新启动模拟来源，查询才能恢复新鲜状态。演示脚本验证六类查询、四类 inspect 成功、重复创建返回同一任务 ID，以及真实订阅；任一步失败就报错，详细证据写入 `results/`。

本工程使用以下本机地址，避免与旧工程的默认数据库端口混用：

| 程序/依赖 | 地址 |
| --- | --- |
| Ingest / Entity / Task / Dispatcher | `127.0.0.1:50051` / `50052` / `50053` / `50054` |
| 四个服务的健康与指标 | `127.0.0.1:18080` 至 `18083` |
| MySQL / Redis / Kafka | `127.0.0.1:13306` / `16379` / `19092` |

每次打开新终端，先加载当前终端需要的凭证与地址：

```powershell
. ./scripts/env.ps1
./bin/opctl.exe snapshot --entity drone-001
./bin/opctl.exe subscribe --entities 'person-001,drone-001' --duration 10s
./bin/opctl.exe task create --entity drone-001 --key my-first-inspect --duration-seconds 1
```

创建响应的 `taskId` 是后续查询需要的实际值：

```powershell
$created = ./bin/opctl.exe task create --entity drone-001 --key another-inspect --duration-seconds 1 | ConvertFrom-Json
if ($LASTEXITCODE -ne 0) { throw 'create failed' }
./bin/opctl.exe task get --id $created.taskId
./bin/opctl.exe task history --id $created.taskId
./bin/opctl.exe dispatch get --task $created.taskId
./bin/opctl.exe dispatcher status --token-env MESHOPS_ADMIN_TOKEN
```

`opctl` 成功输出真实的 Protobuf JSON，错误 JSON 输出到 stderr。退出码 `0` 为成功，`1` 为运行或 RPC 失败，`2` 为命令参数错误。同一个幂等键只能重试相同的标准化创建参数；换参数时使用新键。

停止本次程序，但保留数据库和 bbolt 文件：

```powershell
./scripts/stop.ps1
```

Windows 的停止脚本采用强制进程退出，用于本地操作；服务收到正常退出信号时另有有界优雅退出逻辑。停止脚本按 PID、可执行文件路径和启动时间三者核对身份，不能只凭 PID 杀进程。要停数据库容器可执行 `docker compose stop`。

## 测试

```powershell
./scripts/test.ps1
./scripts/test.ps1 -Integration
./scripts/processes.test.ps1
```

第一条运行单元/本地网络测试及 `go vet`。第二条还会连接真实 MySQL、Redis、Kafka，需要先初始化 Compose；脚本会启动 16380 端口的专用 Redis 故障容器，在这个容器测试 OOM，并在随机测试主题执行 Kafka DeleteRecords。测试使用随机测试库、命名空间或主题，账户需有创建测试库权限。普通测试输出中的集成用例 `SKIP` 表示未执行，不能记为通过。

以下命令依次执行，先停止演示，不要与集成测试并行。故障脚本会实际停止并恢复本 Compose 的 Kafka/Redis/MySQL；压测会创建独立数据库、主题和实体命名空间，保留在 `.local/verification/<run-id>/` 供排查，不清空已有数据：

```powershell
./scripts/stop.ps1
./scripts/faults.ps1
./scripts/benchmark.ps1 -Seconds 30
```

压测先核对 10000 个不同实体快照，再依次运行 100/500 events/s。报告给出实际吞吐、错误、客户端至 Kafka ACK 的延迟、抽样可见延迟、Lag 和进程 Go 堆内存。它不代表订阅扇出、网关落盘或任务执行的性能。每个计时阶段另有三分钟排空预算，整个命令上限十五分钟，超时会返回失败。

安装 [锁定的协议工具](testdata/proto/README.md) 并加入 PATH 后，运行 `./scripts/verify-proto.ps1` 检查 lint、兼容性与生成一致性。独立模块相对原骨架的 Go import 和四个 optional 字段存在有意的源码接口差异，不能直接替换旧生成包。Linux 单元/竞态检查可用 `go test -race ./... -count=1`；需 C/C++ 编译器。[项目 CI](../../../../../.github/workflows/ci.yml)直接验证根目录工程；本地检查不表示远端 Actions 已运行。

课程已有阶段连续复制验证，但全部讲解、操作衔接以及新增搜索后的统一验收仍需完成。代码测试通过不能直接推导“整套课程已验收”。

## 文件与职责

| 目录 | 需要理解的问题 |
| --- | --- |
| `proto/`、`gen/` | 请求/响应和流的边界；前者手写、后者生成 |
| `internal/platform/` | 配置、可信身份、来源绑定、连接和分页签名 |
| `internal/state/` | 原始格式适配、ACK、版本/墓碑、订阅和抽样历史 |
| `internal/bus/` | Kafka 发布确认、分区内顺序处理、手动提交与真实 Lag |
| `internal/tasks/` | 任务事实、事务、Outbox、投递尝试和状态回报 |
| `internal/edge/` | 断线补传队列、执行幂等、取消和模拟副作用 |
| `internal/app/` | 组装依赖、注册 RPC、健康检查和进程生命周期 |
| `internal/cli/` | 操作员真实客户端，包含查询、订阅和任务命令 |
| `migrations/`、`configs/`、`testdata/` | 表结构增量、注册信息、可复现原始输入 |

课程从少量文件开始逐步引入上述结构。此表用于最后回看职责，不要求第一课就创建全部目录。

## 当前排错入口

- `DeadlineExceeded`：核对客户端和配置中的地址，当前课程统一使用 IPv4 回环地址；再查看对应服务日志及 `/readyz`。
- 端口已占用：启动脚本会在启动前拒绝。先确认是否有已有课程进程，使用它所属的停止脚本；不要随意终止其他项目。
- 实体 `found=false`：确认对应来源已接入，查看 `.local/logs/来源名.err.log`；原始数据不会绕过 Ingest 自动写进 Redis。
- 实体已过期：检查来源模拟器是否已结束、网关是否有积压、Kafka 消费是否前进。过期状态仍可查询，不能据此接收新任务。
- 任务停在投递中：查看执行方日志、`dispatch get` 和 `task history`。传输失败会有限重试，不能把“写进 Kafka”当作执行成功。
- Docker Kafka 启动失败：检查 `docker compose logs kafka-init kafka`。Compose 中的初始化容器仅调整 Kafka 专用卷目录的属主，保留已有日志数据。
