# 从零课程：递进代码检查点

本页提供 Z01—Z13 的阶段代码与运行顺序。每个阶段都有完整源码，原项目框架无需复制进来。阶段代码验收与逐课详细讲义分开：Z07 是一个需要多课讲解的阶段，不能把一次复制大量文件当作一节入门课。要先体验完整成品，请直接使用根工程和 [完整前后端启动手册](../../run-fullstack.md)。

逐段讲解从 [新课程目录](lessons/README.md) 进入；手工路线在 Z01—Z03 之后，按 Z04—Z13 的 [28 个小步骤](lessons/steps/README.md)复制精确增删改文件并验证。阶段答案复制脚本管理十三个完整阶段，不管理这些课内小步骤，两条复制路线不要混用。

## 阶段如何累积

| 阶段 | 完整答案 | 本阶段引入的变化 | 此时仍不具备 |
| --- | --- | --- | --- |
| Z01 | [starter/z01](starter/z01/) | module、公共消息、按 ID 查询协议、生成代码 | 没有服务进程 |
| Z02 | [starter/z02](starter/z02/) | 单个 go-zero Entity、固定人员数据、真实查询客户端 | 没有写入和持久存储 |
| Z03 | [starter/z03](starter/z03/) | 临时 PutSnapshot、人员/无人机、带锁内存仓库 | 重启丢失，未做来源鉴权与可靠确认 |
| Z04 | [state-stages/z04](state-stages/z04/) | 原始来源适配、可信来源绑定、统一事件和 Ingest 流 | 使用内存投影，ACK 仍不是磁盘确认 |
| Z05 | [state-stages/z05](state-stages/z05/) | Ingest/Entity 分进程、Kafka ACK、Redis 原子版本投影 | 没有订阅和网关持久补传 |
| Z06 | [state-stages/z06](state-stages/z06/) | 订阅初始化/增量、六类适配、bbolt 队列与 ACK | 没有任务和历史存储 |
| Z07 | [business-stages/z07](business-stages/z07/) | MySQL任务、幂等创建、Outbox、Dispatcher、执行inbox、取消/超时/DLQ、七个程序 | 状态历史未运行，未提供维护恢复/压测入口 |
| Z08 | [business-stages/z08](business-stages/z08/) | 历史抽样、去重/保留清理、分页、完整 opctl | 未提供维护恢复/压测入口 |
| Z09 | [固定阶段答案](business-stages/z09) | 影子恢复、缺口验证、故障/压测工具、第八个程序 verify | 不包含新增搜索、真实硬件、路径规划、最优调度和 AI agent |
| Z10 | [固定搜索阶段](business-stages/z10) | 一个 Search 服务、任务索引、Canal 同步、PIT 分页和维护重建；十个可执行程序 | 没有浏览器接入和正式前端，不包含全平台搜索、搜索容量结论或生产高可用 |
| Z11 | [固定网关阶段](web-stages/z11) | 选定 HTTP 路由、迁移 005 与个人账号、管理员 Setup、浏览器会话、角色与 CSRF、SSE 转换、web-gateway | 尚没有 Vue 页面和完整容器部署 |
| Z12 | [固定前端阶段](web-stages/z12) | 正式 Vue 控制台、个人登录与强制改密、账号管理、真实 API、客户端一致性处理、本地前后端调试 | 尚没有完整 Compose 演示部署 |
| Z13 | [根工程源码](../../..) | 统一镜像与 Compose、机器凭证初始化、管理员自设密码、账号与业务数据持久化和完整运行手册 | 仍是模拟来源/执行方、单机演示，不声称生产高可用或真实设备控制 |

`task history` 是任务状态审计，Z07 已经提供；`opctl history` 是实体状态历史，Z08 才提供。这是两种不同数据。

为保持协议编号与依赖版本稳定，部分后续消息定义、配置类型和历史表 schema 会提前出现；不代表相应功能已经运行。Z04—Z07 尚未实现的历史 RPC 由生成基类返回 UNIMPLEMENTED，Z07 的操作端不接受 `history` 命令。Z07/Z08 均不接受 `--rebuild-view`；Z09 才提供。

各阶段文件的新增、替换、移除列表见 [checkpoint-index.json](checkpoint-index.json)。`source` 是答案目录，`files` 是结束版本的路径与 SHA256，`changes` 是相对上一阶段的变化。讲义编写以这个清单为依据逐一解释，不以“其余类似”跳过必需文件。

## 使用同一个学习目录

以下是默认示例目录。已有同名目录且不为空时，复制脚本会拒绝首次写入；请换一个新的空目录。module 固定为 `example.com/meshops-course`，磁盘目录可以使用其他名字，不需要注册 example.com。

```powershell
$Course = 'D:\job\golang\projects\meshops\docs\learning\from-zero'
$Learner = 'D:\job\golang\projects\meshops-course-lab'
$env:Path = 'D:\go1.25.10\bin;' + $env:Path
$env:GOWORK = 'off'

& "$Course\apply-checkpoint.ps1" -Stage z01 -Destination $Learner
Set-Location $Learner
go build ./...
if ($LASTEXITCODE -ne 0) { throw 'build failed' }
```

完成当前阶段运行和理解后，先停止它的程序，再应用下一阶段。例如：

```powershell
& "$Course\apply-checkpoint.ps1" -Stage z02 -Destination $Learner
Set-Location $Learner
./smoke.ps1
```

脚本只允许当前阶段重放或进入紧接的下一阶段；它检查所有受管理文件的 SHA256，发现学生修改、未受管理文件冲突或路径越界会在写入前拒绝。变更/移除的旧文件保存在 `.course/backups/`；笔记、`.local` 凭证、`data` 队列和容器数据不被检查点替换。若要自己修改，先另存练习分支或副本；不要删掉 `.course/checkpoint.json` 强制继续。

脚本负责文件递进，不自动替学生启动程序或证明功能正常。下面的命令用于运行检查；每课逐段讲解还会解释相应文件为什么出现。

## Z01—Z03：协议到内存读写

Z01 仅有生成包，`go build ./...` 成功证明类型可编译，不是业务测试通过。生成工具说明与原文件见 [Z01讲义](lessons/z01.md)。

Z02、Z03 在阶段目录执行 `./smoke.ps1`。脚本构建并启动本次的临时服务，使用真正的客户端断言输出，结束后只停止自己创建的进程。Z03 验证写入、更新、零电量、非法纬度拒绝以及重启后数据消失。完整解释见 [Z02](lessons/z02.md)、[Z03](lessons/z03.md)。

## Z04：两个来源进入统一事件

在学习目录先执行：

```powershell
. ./scripts/env.ps1
go build -o bin/course.exe ./cmd/course
if ($LASTEXITCODE -ne 0) { throw 'build failed' }
./bin/course.exe serve --role all
```

这是长期运行的服务，放在终端一。终端二也进入 `$Learner`、执行 `. ./scripts/env.ps1`，再执行：

```powershell
./bin/course.exe report --endpoint 127.0.0.1:25151 --source personnel_sim --id person-001 --version 1
./bin/course.exe get --endpoint 127.0.0.1:25151 --id person-001
./bin/course.exe report --endpoint 127.0.0.1:25151 --source drone_sim --id drone-001 --version 1
./bin/course.exe get --endpoint 127.0.0.1:25151 --id drone-001
```

查询应返回 found=true、对应类型和 version=1。第二次更新同实体要递增 version；同版本却生成不同内容会冲突。命令默认生成当前时间的原始输入，因此不会因磁盘 fixture 时间很旧而立即过期。Z04 ACK 表示内存接收成功；关闭服务后数据丢失是本阶段边界。

## Z05—Z06：持久链路和六类来源

先启用 Docker Desktop Linux engine。两个阶段使用专用 `meshops-state-lessons` 容器：Redis26379、Kafka29092；不使用根框架的数据库。

```powershell
docker compose up -d --wait --wait-timeout 180
. ./scripts/env.ps1
go build -o bin/course.exe ./cmd/course
if ($LASTEXITCODE -ne 0) { throw 'build failed' }
./bin/course.exe serve --role ingest -f configs/server.yaml
```

终端二进入同一目录、载入 env 后启动 `./bin/course.exe serve --role entity -f configs/entity.yaml`。

Z05 的上报地址为25251、查询地址为25252；Z06 分别为25351、25352，完整 IP 均为127.0.0.1。Z05 按 Z04 的命令替换地址即可上报查询；ACK 先于消费投影，所以立刻查询可以暂时 found=false，稍后应能看见。

Z06 用持久网关分配序号，不再手动创建同来源的版本。下面演示人员离线产生事件再补传：

```powershell
go build -o bin/gateway-simulator.exe ./cmd/gateway-simulator
if ($LASTEXITCODE -ne 0) { throw 'build failed' }
./bin/gateway-simulator.exe --manifest configs/simulation.yaml --source personnel_sim --db data/personnel_sim.db --offline --rate 10 --duration 10s
./bin/gateway-simulator.exe --manifest configs/simulation.yaml --source personnel_sim --db data/personnel_sim.db --endpoint 127.0.0.1:25351 --drain-only --timeout 60s
./bin/course.exe get --endpoint 127.0.0.1:25352 --id person-001
./bin/course.exe watch --endpoint 127.0.0.1:25352 --id person-001
```

其余来源 ID 是 drone_sim、vehicle_sim、robot_sim、sensor_sim、facility_sim，分别使用自己的 DB 文件；实体 ID 分别为 drone-001 等。不要对已使用过的同一来源删除 DB 后从 version=1 重来。订阅先出现 SNAPSHOT_END，之后才按初始化结果应用增量；当前 course watch 是15秒观察工具，结束时报告 deadline，完整 opctl 的正常时长退出在 Z07 提供。

两个阶段都可以执行 `go test ./... -count=1`；若要验证真实 Kafka/Redis 测试，先在当前终端设置：

```powershell
$env:MESHOPS_TEST_REDIS_ADDR = '127.0.0.1:26379'
$env:MESHOPS_TEST_KAFKA_BROKERS = 'localhost:29092'
go test ./internal/state ./internal/bus -count=1 -v -timeout=3m
if ($LASTEXITCODE -ne 0) { throw 'integration failed' }
```

升级前在两个服务终端按 Ctrl+C，随后 `docker compose stop`。Z05/Z06 的测试主题和 Redis 活跃视图按阶段隔离，不把两个阶段当成已经迁移了全部旧数据。

## Z07—Z09：任务、历史和验收

这三个阶段使用最终 `meshops-course` 专用环境（MySQL13306、Redis16379、Kafka19092）。从 Z06 进入 Z07 时重新 seed 并生成状态；不会自动把早期隔离演示数据搬入新环境。Z07→Z08→Z09 则保留同一个环境、凭证和本地队列。

每次应用新阶段后，在学习目录执行：

```powershell
./scripts/initialize.ps1
try {
    ./scripts/start.ps1 -Simulators
    ./scripts/demo.ps1
} finally {
    ./scripts/stop.ps1
}
```

演示验证六类状态、四类执行者的 inspect、创建幂等、真实订阅和结果 effect_count=1。若要继续手工查询，启动模拟器后先不要执行 stop；每个新终端载入 `. ./scripts/env.ps1`。

Z08 起可查询最近状态历史：

```powershell
. ./scripts/env.ps1
$from = [DateTimeOffset]::UtcNow.AddMinutes(-10).ToString('O')
$to = [DateTimeOffset]::UtcNow.ToString('O')
./bin/opctl.exe history --entity person-001 --start $from --end $to --page-size 10
```

Z09 的完整运行、恢复和压测命令见 [最终工程说明](../../../README.md)。已有参考工程运行过默认 `meshops-course` 环境时，不要直接用另一套新凭证 seed 同一数据库；继续使用原来的学习目录，或采用单独的 Compose 项目与端口。教师连续验证脚本使用独立数据卷，期间释放再恢复固定端口。

## 维护与证据

Z09 → Z10 在同一学习目录停止服务后升级，保留 `.local`、data 及 Compose 业务卷。受管理路线运行 `apply-checkpoint.ps1 -Stage z10 -Destination 学习目录`；手工路线按三个新增步骤复制。构建后执行 `initialize-search.ps1`、`start.ps1 -Search -Simulators`、`demo-search.ps1`，具体解释和恢复命令见 [Z10](lessons/z10.md)。不要在已完成搜索初始化之后反复重新 seed 原业务数据库。

Z10 → Z11 → Z12 → Z13 继续使用同一学习目录，先停止正在运行的对应程序，再应用紧接的下一阶段。Z11 增加网关但浏览器根路径还没有 Vue；Z12 启动 Vite 后访问 `localhost:5173`；Z13 的完整 Docker 页面入口为 `localhost:18090`。这两种模式占用的宿主网关端口会冲突，不能同时运行。

完整前后端的安装工具、启动顺序、个人账号初始化、日志、停服务、恢复和切回 IDE 统一以 [启动手册](../../run-fullstack.md) 为准。Z11 先停止旧 Go 服务，按[本课步骤](lessons/z11.md)完成迁移 005 和后端重建，再在学习工程根目录使用 `./scripts/account-admin.ps1 -Mode Local -Action Setup -Username admin` 安全设置管理员密码；操作员由管理员创建，首次登录必须改密。根 `meshops` 是完整参考成品，兄弟目录 `meshops-course-lab` 是你自己的实现；不要跨目录混用 `.local` 身份或数据库初始化，也不要把整个参考仓库当作学员首次复制内容。

新增五个分步的复制与构建可以由维护工具 `verify-substeps.ps1 -WebOnly` 检查；`verify-file-guides.ps1 -Build -Frontend` 还会从讲义恢复文件并构建前后端。它们不启动完整 Docker 业务演示，也不自动完成真实浏览器视觉/键盘验收。必须分别记录复制、测试、部署和交互结果。

- [materialize-checkpoints.ps1](materialize-checkpoints.ps1)：教师同步经过验收的共享代码并生成 Z07/Z08；学生直接拿完整文件，不需要先运行它。
- [test-checkpoints.ps1](test-checkpoints.ps1)：检查复制、跳级、学生改动、笔记保留、备份与路径边界。
- [verify-checkpoint-chain.ps1](verify-checkpoint-chain.ps1)：从空目录按阶段运行的维护验收，使用独立数据卷；会暂时停止并恢复参考依赖，正常学习无需运行它。
- [本次阶段验证记录](verification/2026-09-10-checkpoints.md)：结果、运行边界及后续交接。

不要运行旧 `state-stages/materialize.py`：它已停用，避免用旧规则覆盖新检查点。
