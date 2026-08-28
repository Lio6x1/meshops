# 多源实体实时协同与可靠任务调度平台

**项目代号：MeshOps**，取自 **Multi-source Entity Collaboration & Operations**，表示“多源实体协同与运行管理”。简历和正式文档统一使用中文名称“多源实体实时协同与可靠任务调度平台”，MeshOps 只作为仓库名和工程代号。

本项目面向 Go 后端校招展示。平台不直接管理无人机、车辆或传感器等硬件，也不实现 MQTT、OTA、飞控和设备影子；它接收边缘网关、仿真系统和业务集成服务已经标准化的数据，统一维护实体最新状态，并向操作端可靠地下发和跟踪任务。

项目参考 Lattice 的 Entity、Component、Provenance、TaskCatalog 和版本化任务状态模型，但重点是可复现的互联网后端工程问题：突发事件流削峰、分区有序处理、最新值投影、实时订阅背压、事务消息、消费者幂等、任务并发控制、故障恢复与可观测性。

> **当前状态：P0 工程骨架。** 已完成需求分析、架构决策、Protobuf 契约、go-zero 服务骨架、数据库初始迁移和本地依赖配置；业务逻辑、自动化测试、压测与故障演练尚待按阶段实现。下文描述的是目标能力，不代表已经完成或验证。

## 目标业务闭环

1. 最小参考边缘网关生成标准化实体事件，先写入基于 bbolt 的本地持久化缓冲，再通过 gRPC 客户端流批量上报；链路恢复后按 ACK 水位限速补传。
2. 接入服务完成认证、限流、协议校验和幂等检查，并按 `entity_id` 写入 Kafka。
3. 状态投影器消费事件，依据版本号更新 Redis 最新状态快照，并按策略向 MySQL 写入历史抽样。
4. 实时订阅服务第一阶段通过进程内有界队列向操作端推送；多实例阶段才启用 Redis Pub/Sub。断线重连时先拉快照，再继续增量订阅。
5. 操作端创建任务；任务服务在一个 MySQL 事务中写入任务与 Outbox 事件。
6. Outbox 投递器将任务事件投递到 Kafka；任务分发器消费后下发任务并处理 ACK、超时、重试和死信。
7. 执行方回报任务状态，任务服务使用状态机、幂等键和乐观锁解决并发更新。

## 双数据语义

| 数据 | 业务语义 | 主要保证 | 技术路径 |
| --- | --- | --- | --- |
| 实体状态事件 | 高频；允许展示端丢失中间帧；需要短期回放和重建 | Kafka 分区内有序、至少一次消费、消费者幂等 | gRPC → Kafka → Redis最新快照 → 有界队列；多实例时增加Pub/Sub |
| 任务与审计事件 | 低频但不可静默丢失、不可重复执行 | MySQL 事务、Transactional Outbox、至少一次投递、幂等消费 | MySQL + Outbox → Kafka → 任务分发器 |

Kafka是可靠事件主干；MySQL是任务、配置和审计的事实来源；Redis是可重建的派生状态。Redis Pub/Sub只在实时订阅多实例后用于可恢复的低延迟通知，不承担可靠消息职责。

## 技术栈

- Go、go-zero 服务骨架、Protobuf/grpc-go 流式通信
- Kafka (KRaft模式)：状态事件、任务事件、消费位点、重放、削峰和多消费者；校招项目使用单Broker KRaft模式
- Redis：实体最新快照、限流和热点查询；Pub/Sub仅在实时订阅多实例阶段启用
- MySQL：实体元数据、任务、Outbox、审计和历史抽样
- bbolt：参考边缘网关本地持久化缓冲与 ACK 水位
- Prometheus、结构化日志；Grafana和OpenTelemetry在核心稳定后启用
- Docker Compose、GitHub Actions、自研Go负载模拟器；ghz仅辅助测试Unary RPC
- etcd：**P3阶段**在至少两个真实服务实例下实现动态注册发现；P0—P2使用静态Endpoint
- Elasticsearch、ClickHouse不属于当前实施技术栈，只保留基于实测的启用门槛

## 术语说明

| 术语 | 定义 | 测量边界 |
| --- | --- | --- |
| 受理延迟 | gRPC接收请求到Kafka返回ACK的时间 | 接入服务内部指标 |
| 可见延迟 | 事件产生时刻到Redis快照更新完成的端到端时间 | 客户端埋点到查询验证 |
| 投影延迟 | 从Kafka消费到Redis更新完成的时间 | 状态投影器内部指标 |
| 补传 | 网关链路恢复后，从bbolt读取积压事件并限速上报 | 区别于实时上报 |
| 幂等 | 重复请求不产生重复业务结果 | 不等于去重；允许执行多次但结果一致 |

## 设计与压测目标

以下是项目目标，不是未经验证的生产成绩：

- 20～100 个集成数据源或边缘网关。
- 1 万～10 万活跃实体。
- 稳态 1,000～5,000 events/s；重连补报目标 20,000～50,000 events/s。
- 20～50 个实时订阅者，压力测试扩展到 200。
- 任务创建峰值目标 50～100/s。
- 状态写入受理 P99 小于 100ms；实时可见 P99 小于 200ms。

所有结果必须记录机器环境、数据集、并发模型和原始输出。简历只能引用已经通过自动化测试、压测或故障演练验证的能力。

## 历史回放边界

- Kafka 在配置的保留期内支持系统内部事件重放，用于重建 Redis 快照、修复消费者和新增下游。
- 第一阶段由历史抽样写入器将状态按固定周期、显著变化或任务关键节点抽样写入 MySQL，提供基础轨迹查询和回放。
- 完整长期原始轨迹、复杂地理检索和大规模聚合不进入核心阶段；需要时再增加 ClickHouse 或 Elasticsearch 消费者。

## 非目标

- 不实现硬件接入协议、MQTT、OTA、飞控、图传和设备影子。
- 不宣称真实军用部署、真实生产 SLA 或未经验证的十万 QPS。
- 不为了展示名词同时引入 Redis Streams、RabbitMQ、NATS 和 Kafka。
- 不自研WAL；参考网关使用bbolt实现本地持久化缓冲队列。
- 第一阶段不引入 Kubernetes、服务网格和完整 LLM Agent。
- 后续 Agent 只能通过受控 Tool Gateway 调用应用服务，禁止直接访问数据库或通用命令执行器。

## 工程结构

```text
meshops/
├── proto/                     # Protobuf 源文件
│   ├── common/v1/             # 共享消息（EntityStateEvent、Task 等）
│   ├── ingest/v1/             # 接入服务 RPC 契约
│   ├── entity/v1/             # 实体服务 RPC 契约
│   ├── task/v1/               # 任务服务 RPC 契约
│   └── dispatcher/v1/         # 分发器管理接口 RPC 契约
├── gen/                       # 代码生成产物（不手动修改）
│   ├── common/v1/             # 共享消息 pb.go（由 protoc 生成）
│   ├── ingest/v1/             # 接入服务 pb.go + grpc.pb.go
│   ├── entity/v1/             # 实体服务 pb.go + grpc.pb.go
│   ├── task/v1/               # 任务服务 pb.go + grpc.pb.go
│   └── dispatcher/v1/         # 分发器服务 pb.go + grpc.pb.go
├── app/                       # goctl 生成的 zRPC 服务骨架
│   ├── ingest/                # 接入服务（认证/限流/Kafka生产）
│   │   ├── etc/               # 服务配置文件
│   │   ├── internal/
│   │   │   ├── config/        # 配置结构体
│   │   │   ├── logic/         # 业务逻辑（填写 todo）
│   │   │   ├── server/        # gRPC server 注册
│   │   │   └── svc/           # ServiceContext（依赖注入）
│   │   └── client/            # gRPC 客户端封装
│   ├── entity/                # 实体服务（投影/快照/实时订阅）
│   ├── task/                  # 任务服务（状态机/Outbox）
│   └── dispatcher/            # 任务分发器管理接口
├── configs/                   # 配置示例（config.example.yaml）
├── migrations/                # 数据库版本化迁移（001_initial_schema.sql）
├── deployments/               # Prometheus / Grafana 部署配置
├── scripts/
│   ├── proto-gen.sh           # proto 代码生成（方案A：protoc+goctl+alias修复）
│   ├── benchmark/             # 压测脚本
│   └── chaos/                 # 故障演练脚本
├── docker-compose.yml         # 本地一键启动 Kafka(KRaft)/Redis/MySQL/etcd
└── Makefile                   # 常用命令封装
```

计划在对应阶段新增 `proto/executor/v1/`、`cmd/gateway-simulator/`、`cmd/executor-simulator/` 和 `cmd/opctl/`；这些目录当前尚未实现。

### proto 生成策略（方案A）

- **共享消息** (`proto/common/v1/`) → 用 `protoc` 生成到 `gen/common/v1/`，全项目单一真相源
- **服务契约** (`proto/{svc}/v1/`) → 用 `goctl rpc protoc` 生成 zRPC 骨架到 `app/{svc}/`
- `gen/` 下生成代码提交到仓库；新克隆可以直接构建，修改 Proto 后需同时提交重新生成的代码

```bash
# 生成所有（包含 alias bug 自动修复）
make proto

# 只重新生成某个服务
make proto-ingest
make proto-entity
```



## 快速开始

```bash
# 1. 下载依赖并验证当前骨架
make deps
make build
make test

# 2. 启动基础依赖（P0-P2）
make up                       # docker compose up -d kafka redis mysql

# 3. 应用数据库迁移
make migrate

# 4. 安装开发工具并运行提交前检查
make tools                    # 安装 goctl、staticcheck
make check                    # go vet + staticcheck + go test -race

# 修改 Proto 时才需要安装 protoc 及 Go 插件，然后重新生成
make proto

# P3 阶段（含 etcd）与监控
make up-p3                    # 启动 etcd
make up-all                   # 启动完整环境含 Prometheus + Grafana
```

应用服务端口：Ingest `50051`、Entity `50052`、Task `50053`、Dispatcher `50054`。

依赖服务端口：Kafka `9092`、Redis `6379`、MySQL `3306`、etcd `2379`、Prometheus `9090`、Grafana `3000`。宿主机 Kafka 客户端使用 `localhost:9092`，Compose 网络内客户端使用 `kafka:29092`。

## 文档

- [需求分析](docs/superpowers/specs/2026-08-24-meshops-requirement-analysis.md)
- [总体架构与开发规划](docs/superpowers/specs/2026-08-24-meshops-system-design.md)
- [技术选型评审](docs/superpowers/specs/2026-08-25-meshops-technology-selection-review.md)
- [生产就绪检查清单](docs/production-readiness-checklist.md)
- [架构决策记录](docs/adr)
