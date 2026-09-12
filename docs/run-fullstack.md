# 完整前后端：第一次启动、体验与排错

这份手册面向第一次运行前后端项目的读者。你不需要先自己实现业务代码，也不需要先学完 Vue。参考工程 `meshops` 已放置前端、HTTP 网关、Go 服务和部署文件；按下面的顺序启动，再通过网页观察真实后端处理结果。

教程从零编写的位置是另一个目录 `meshops-course-lab`。第一次体验请用完整参考工程，**不要把 `.local` 凭证、`data` 队列或 Docker 数据卷复制到学习目录**。之后按 [课程目录](learning/from-zero/lessons/README.md) 从 Z00 开始，在同一个学习目录逐步实现。

## 1. 先选择启动方式

| 方式 | 工具要求 | 页面地址 | 推荐用途 |
| --- | --- | --- | --- |
| 完整 Docker 演示 | Git 或已下载的源码、Docker Desktop、PowerShell | `http://localhost:18090` | 先看完成品、展示完整流程 |
| 本地学习调试 | 以上工具，加 Go、Node.js（含 npm） | `http://localhost:5173` | 修改 Go/Vue、看日志、在 IDE 打断点 |

前端是浏览器显示和操作的页面；后端是处理请求、保存事实、投递任务的进程；数据库和消息队列为后端提供存储与通信。两种方式执行的是同一套业务代码，配置和进程运行位置不同。

```text
Docker：浏览器 :18090 → Nginx 页面及 /api 代理 → HTTP 网关 → gRPC 后端
调试：  浏览器 :5173  → Vite 页面及 /api 代理  → 本机 :18090 网关 → gRPC 后端
```

两种方式的宿主 `18090` 会冲突。**同一时间选一种方式运行**；切换前按本文停止原来那套进程。Docker 演示固定使用 `meshops-demo` 项目名和独立卷，学习调试沿用课程的中间件项目；它们的数据和浏览器访问码不是同一套。

Docker 中只有提供网页的 `web` 容器同时连接普通 `frontend` 网络与 `private` 内部网络，负责宿主端口发布和转发到网关。其余服务仅在 `private` 通信；数据库、Kafka、ES、gRPC 和网关没有宿主端口。不要把 web 也改成只连接 `internal: true` 网络，否则可能出现容器内健康而宿主网页不可达的问题，见[本轮网络问题记录](postmortems/2026-09-12-fullstack.md#5-容器全部-healthy宿主网页端口却拒绝连接)。

## 2. 准备源码与终端

如果这台电脑已有 `D:\job\golang\projects\meshops`，直接打开它，不要重复 clone。新电脑可在 PowerShell 执行：

```powershell
New-Item -ItemType Directory -Force 'D:\job\golang\projects' | Out-Null
Set-Location 'D:\job\golang\projects'
git clone https://github.com/Lio6x1/meshops.git
Set-Location 'D:\job\golang\projects\meshops'
Get-Location
Test-Path './compose.demo.yml'
```

`Get-Location` 应显示工程根目录，`Test-Path` 应为 `True`。如果下载的是 ZIP，先解压，进入能看到 `go.mod`、`web`、`scripts`、`compose.demo.yml` 的那一层目录。GitHub 代码以实际已发布提交为准，下载后先确认包含这些前后端文件。

打开 PowerShell 可以在资源管理器的项目文件夹空白处右键选择“在终端中打开”，也可以在 GoLand/VS Code 的终端中运行相同命令。以下每个代码框只复制内部命令，不复制 Markdown 的三反引号。

若可信脚本被执行策略阻止，按照 [PowerShell 命令约定](learning/from-zero/README.md#powershell-命令约定) 处理。不要修改企业组策略；当前进程的执行策略设置在关闭终端后失效。

## 3. Docker 模式：首次启动

先安装并打开 Docker Desktop，切换到 Linux containers，等待引擎启动完成。然后检查：

```powershell
docker version
docker compose version
```

第一条应同时显示 Client 和 Server 信息。如果只有 Client 或提示无法连接引擎，先解决 Docker Desktop 启动问题，不要继续把服务连接错误解释成业务代码错误。

首次会下载 Go/Node/数据库等镜像并构建程序，需要可用网络、磁盘和足够的 Docker 内存。下载很慢时看终端当前停在哪个镜像；内存不足时检查 Docker Desktop 的资源配置和容器日志。本文不把未经测量的内存容量当作硬性最低要求。

在工程根目录运行：

```powershell
./scripts/demo-stack.ps1 -Action Up
./scripts/demo-stack.ps1 -Action Status
```

`Up` 不是只启动一个网页。它按顺序完成：构建镜像、首次生成随机凭证、启动 MySQL/Redis/Kafka/ES、在业务写入及 Canal 停止的窗口执行迁移与搜索初始化，再启动网关、五个业务服务、前端及模拟来源/执行方。再次运行会复用卷中的数据和凭证，必要的初始化步骤会重新检查现状。

看到 `Demo is running` 后，仍须完成第 5 节的业务演示。健康检查成功不等于已验证任务执行和 CDC 搜索。`Status` 中 `init`、`kafka-init` 这样的初始化进程成功退出是正常现象；常驻服务反复 Restarting、Exited 非零或 unhealthy 则需查看对应日志。

`Up` 会在成功提示前自动探测宿主的 `/healthz` 和网页 `/`，每项超时五秒；任何一项不可访问或不是 `200` 都会报错并保留容器与数据卷，方便排查。需要单独复核时，在当前宿主终端执行下面两项，都应输出 `200`：

```powershell
(Invoke-WebRequest 'http://127.0.0.1:18090/healthz' -UseBasicParsing -TimeoutSec 5).StatusCode
(Invoke-WebRequest 'http://127.0.0.1:18090/' -UseBasicParsing -TimeoutSec 5).StatusCode
```

这一步检查容器外的真实 HTTP 路径，不能用容器内部 `Healthy` 替代。

读取浏览器访问码：

```powershell
./scripts/demo-stack.ps1 -Action Codes
```

只将对应角色的演示访问码填入登录页面。它不是数据库密码或 gRPC 机器令牌，命令不会要求你把后端凭证贴进网页。访问码是本机登录秘密，不要截进公开演示图片或提交 Git。

浏览器打开 **`http://localhost:18090`**，先选择“操作员”并输入 operator 对应访问码。也可使用 `http://127.0.0.1:18090`，但两个主机名的 cookie 独立，切换地址可能需要重新登录。

代码和镜像没有改动时，再次启动可以省略构建：

```powershell
./scripts/demo-stack.ps1 -Action Up -NoBuild
```

`-NoBuild` 仍会执行初始化检查和服务启动，不是绕过健康检查。首次没有本地镜像、或刚修改代码后，不使用此参数；否则可能运行不到你的新代码。

## 4. Docker 模式：停止、日志和重置

日常停止并保留数据：

```powershell
./scripts/demo-stack.ps1 -Action Stop
```

删除本演示容器和网络，但保留命名卷：

```powershell
./scripts/demo-stack.ps1 -Action Down
```

之后都用 `Up` 再启动。服务端会话保存在内存中，重启网关后需要重新登录；访问码保存在卷中，普通 Stop/Down 不会替换它。

查看所有日志或某个服务：

```powershell
./scripts/demo-stack.ps1 -Action Logs
./scripts/demo-stack.ps1 -Action Logs -Service task
./scripts/demo-stack.ps1 -Action Logs -Service gateway -Follow
```

`-Follow` 持续打印后续日志，用 `Ctrl+C` 结束“看日志”，不会停止服务。`-Service`、`-Follow` 只与 `Logs` 一起使用。常用服务名还包括 `web`、`entity`、`dispatcher`、`search`、`canal`、`source-drone` 和 `executor-drone`。

只有你确实想删除这套演示的所有数据库、消息、队列及访问码，才执行下面的独立操作；它不是排错的默认第一步：

```powershell
./scripts/demo-stack.ps1 -Action Reset -ConfirmReset
./scripts/demo-stack.ps1 -Action Up
./scripts/demo-stack.ps1 -Action Codes
```

脚本限定在 `meshops-demo` 项目范围，`Reset` 会删除该项目的卷；下一次启动生成新数据和新访问码。它不会替你备份旧任务。省略 `-ConfirmReset` 时脚本会拒绝执行。

### 只修复搜索，不重置业务数据

如果已经确认搜索引导未完成、ES 索引丢失或被替换，或 Canal 因任务表 DDL/过期位点而停止推进，使用独立搜索恢复入口。先查看 `search` 和 `canal` 日志，解决依赖不可达、磁盘不足等根因；运行下列命令时 MySQL、Kafka、ES 应已经启动，镜像应与当前代码一致：

```powershell
./scripts/demo-stack.ps1 -Action RebuildSearch
./scripts/demo-stack.ps1 -Action Status
./scripts/demo-stack.ps1 -Action Logs -Service search
./scripts/demo-stack.ps1 -Action Logs -Service canal
```

`RebuildSearch` 不构建镜像，也不执行迁移/seed。它与启动、停止和重置共用同一把本地维护锁；只停止 Search 与 Canal，原有实体上报、任务处理和网页仍可运行，搜索页暂时报告不可用。工具重新从 MySQL 任务事实导入专用 ES 索引，设置新的 CDC 起点；快照锁仅在建立一致视图期间短暂持有。

新快照完整标记写入成功后，才重建 Canal 容器，并清理专属元数据目录中已经核实的 `meta.dat`、`h2.mv.db`、`h2.trace.db`、`h2.lock.db`。发现未知文件、子目录或符号链接会停止，让你先检查实际版本与配置。它不删除 MySQL 事实表、不删除 Kafka topic、不删除任何命名卷，也不改变访问码。

任何恢复步骤失败，工具会再次停止 Search/Canal 并将标记设为未完成。修复日志指出的问题后重跑同一命令；不要手工填写 `complete=true`，也不要通过全量 `Reset` 掩盖搜索问题。恢复成功后，用页面检查已有任务可搜索，再创建带新备注的任务，确认新的 CDC 更新也能进入搜索；只看到 Search 健康不能证明增量链路恢复。

凭证保存在同一专属卷内，但按 Linux 身份分文件授权：完整引导凭证只给 root；普通 Go 服务只读取公共可信 Registry 机器令牌、游标密钥与应用数据库密码；网关独立 UID 额外读取浏览器访问码；MySQL 单独读取 root 密码文件。普通服务仍共享演示 Registry 的机器身份，这不是生产环境中的完整服务级秘密隔离。再次初始化会核对派生凭证文件内容，不会静默覆盖不一致的文件。

## 5. 第一次体验按这个顺序走

1. **实体总览**：等待右上方显示“已连接 · 同步完成”。检查人员、无人机、车辆、机器人、传感器、设施六种类型都有真实注册实体，快照时间持续更新。连接状态、数据新鲜度、业务状态是三个不同概念。
2. **实体详情与历史**：打开 `drone-001`，看经纬度、电量、来源和版本；再查看人员或设施，确认字段随类型变化。历史默认查最近一小时，需要点击“查询历史”；历史是采样记录，不承诺完整轨迹。
3. **创建任务**：在任务中心点击“创建巡检任务”，选择新鲜可执行实体，时长填写 `5` 秒，备注填写你容易记住的文字，例如 `首次网页巡检-东门`，截止时间保持默认。传感器和设施不会成为执行选项。
4. **跟踪结果**：创建成功自动进入详情。查看任务状态、执行键、执行器和状态记录，最后确认 `已完成` 与执行结果。每 2 秒刷新可能跨过很短的中间状态，持久化记录用来补充追踪。
5. **取消意图**：另建一个 `30` 秒任务，尽快在详情点“请求取消”并填写原因。页面先显示“已请求取消，等待确认”；只有服务端最终状态为 CANCELLED 才显示“已取消”。如果任务已完成，取消入口会消失，这是正常竞态结果。
6. **任务检索**：用第 3 步的备注搜索，稍后应看到对应任务。打开结果会查询任务权威详情。搜索短暂落后属于 CDC 链路的最终一致性；请求报错和成功但空结果要分开处理。
7. **可靠分发**：从详情进入分发页，核对最新单条尝试的任务 ID、分发 ID、执行键与传输状态。不要把“已分发”当成“已执行完成”。
8. **管理员入口**：退出后选择管理员并使用 admin 访问码。运行状态显示分发器真实的三个采样指标。仅真实记录处于 `dlq` 时人工重试按钮可用；如果没有死信，不需要制造或修改数据库来凑演示。专用故障演练由验收步骤单独安排。

若创建响应因断网或超时不确定，页面保留完整请求和幂等键。使用“重试原请求”，先核对任务中心，不要反复生成新的任务请求。刷新或离开页面会丢失内存中的未确认草稿，页面会提示你先确认。

## 6. 本地学习模式：前后端分别怎样启动

此方式在 Windows PowerShell 中执行。先停止 Docker 完整演示，释放宿主 `18090`：

```powershell
./scripts/demo-stack.ps1 -Action Stop
```

安装 Go（参考项目锁定和验证版本）、Node.js（含 npm，建议与当前项目一致使用 24.15，至少 22.12）并重新打开终端，使 PATH 生效。Docker 构建中的 Go/Node 来自 Dockerfile，不要求与本机二进制路径相同。检查：

```powershell
go version
node --version
docker compose version
```

### 终端 A：初始化并启动后端

参考工程用 `meshops`；自己的完整学习工程则将下面路径换为 `meshops-course-lab`。课程中间件使用固定端口和同一套课程数据库，**不能让两个目录的学习后端同时运行或拿不同凭证 seed 同一数据库**。先用原目录的 `stop.ps1` 和 `stop-web.ps1` 停掉旧进程，确定当前只运行你选择的一个目录。

第一次在该目录初始化完整后端：

```powershell
Set-Location 'D:\job\golang\projects\meshops'
./scripts/initialize.ps1
./scripts/initialize-search.ps1
go build -o bin/web-gateway.exe ./cmd/web-gateway
if ($LASTEXITCODE -ne 0) { throw 'web gateway build failed' }
./scripts/start.ps1 -Simulators -Search
./scripts/start-web.ps1
```

`initialize.ps1` 创建本目录课程凭证、启动中间件、构建程序并执行迁移/seed；`initialize-search.ps1` 完成搜索依赖、快照与 Canal 初始化。最终阶段 `build.ps1` 已包含网关；上面的网关构建命令仍显式列出，便于理解 `start-web.ps1` 使用的实际二进制。单独 `go build ./...` 不会把可运行文件放进 `bin`。

如果该目录的 Z10 后端已经完成初始化，不要重新执行所有初始化步骤。正常关闭后再次启动可用：

```powershell
. ./scripts/search-env.ps1
docker compose -f docker-compose.yml -f compose.search.yml --profile search start
if ($LASTEXITCODE -ne 0) { throw 'start existing dependencies failed; inspect Compose status' }
./scripts/build.ps1
go build -o bin/web-gateway.exe ./cmd/web-gateway
if ($LASTEXITCODE -ne 0) { throw 'web gateway build failed' }
./scripts/start.ps1 -Simulators -Search
./scripts/start-web.ps1
```

这里的 `start` 恢复已经创建的容器，不创建缺失容器。如果你曾删除容器、改变依赖配置或搜索标记不完整，回到对应初始化/维护章节处理，不要伪造 `complete=true` 标记。

启动脚本会把 Go 进程放到后台，所以终端 A 可以继续输入命令；日志在 `.local/logs/`。来源模拟器本地默认上报 30 分钟，超过后快照逐渐过期，需要停止后重新启动模拟器。Docker 完整演示的来源命令配置为 24 小时，二者不要混淆。

### 终端 B：安装并运行前端

打开第二个 PowerShell，进入同一个工程目录：

```powershell
Set-Location 'D:\job\golang\projects\meshops'
./scripts/frontend.ps1 -Action Install
./scripts/frontend.ps1 -Action Dev
```

`Install` 实际执行 `npm ci`，按 `web/package-lock.json` 安装精确依赖；只在首次或锁文件变化后执行。`Dev` 执行 Vite 开发服务器，正常情况下终端保持运行，并显示 `http://127.0.0.1:5173`。不要等待它自动退出；保留此终端，用浏览器打开 `http://localhost:5173`。

本机曾出现旧 `npm.ps1` 指向已删除 npm 的问题，因此脚本使用所选 Node 安装目录里随附的 npm CLI；仍要求 Node 安装时包含 npm。脚本报找不到 npm CLI 时修复 Node 安装，不要修改业务代码。

开发模式访问码位于当前工程的 `.local/web-secrets.json`，用本地编辑器查看其中 operator/admin 对应值即可。它与 Docker 模式 `Codes` 输出不是同一套，不要混用。

### 修改后怎样生效

- 修改 `web/src`：Vite 通常自动更新页面；若错误覆盖层出现，先修复编译错误。修改依赖后重新 Install，必要时重启 Dev。
- 修改 Go：停止对应进程，重新编译，再启动。脚本运行的是 `bin/*.exe`，保存 `.go` 文件不会自动替换正在运行的程序。
- 在 GoLand/VS Code 调试：先停止脚本启动的同一服务，避免端口冲突；工作目录设工程根，入口选 `cmd/<服务名>`，参数按 `scripts/start.ps1` 的 `-f configs/<服务名>.yaml`。调试器还须加载当前工程凭证环境；普通 IDE Run 不会自动继承另一个独立终端的 `. ./scripts/env.ps1`。
- 切回 Docker：先停止本地前后端，再运行 Docker `Up`。Docker 代码变更后需要重新构建，不使用 `-NoBuild`。

### 学习模式怎样停止

终端 B 按 `Ctrl+C` 停止 Vite。终端 A 执行：

```powershell
./scripts/stop-web.ps1
./scripts/stop.ps1
. ./scripts/search-env.ps1
docker compose -f docker-compose.yml -f compose.search.yml --profile search stop
```

这些命令保留数据库、搜索索引、凭证和本地队列。Windows 停止脚本会按 PID、路径、启动时间核对所拥有的进程；不要改成批量杀所有 `go` 或同名进程。

## 7. 出错先看哪一层

Docker 构建若明确报 Go 模块代理 DNS/连接超时，可以在当前终端显式选择可达的代理再重试，默认构建使用官方代理，校验和仍开启：

```powershell
$env:MESHOPS_BUILD_GOPROXY = 'https://goproxy.cn,https://proxy.golang.org,direct'
./scripts/demo-stack.ps1 -Action Up
```

这只处理 Go 模块来源，不会自动修复镜像仓库或 npm 网络问题。不要关闭 TLS 或 Go 校验和验证；保留最初失败日志以确定真正失败的下载步骤。

本次本机还遇到 Docker 构建阶段不能直接解析/访问 Go 模块下载地址。核对 `docker info` 中 Docker Desktop 已配置的代理后，将该代理显式传给本次构建，Go 依赖下载和镜像构建才成功。这台机器核实的地址为 `http://http.docker.internal:3128`，对应命令是：

```powershell
./scripts/demo-stack.ps1 -Action Up -BuildProxy 'http://http.docker.internal:3128'
```

**这不是所有电脑通用的代理地址。** 其他电脑仅在已配置且验证可达的代理存在时，填写自己的 HTTP(S) 代理 URL；没有代理且直接下载正常就用普通 Up。`-BuildProxy` 只用于启用构建的 Up，不与 `-NoBuild` 合用，不包含代理用户名/密码。它给构建阶段传 `HTTP_PROXY/HTTPS_PROXY`，不把代理写入业务服务运行配置，也不关闭证书或校验和验证。

| 现象 | 首先检查 | 解释 |
| --- | --- | --- |
| 页面根本打不开 | Docker `Status`/`Logs -Service web`；或终端 B 的 Vite 输出 | 先确认访问 18090 还是 5173、进程是否在运行 |
| 登录后 401 | 地址主机名、角色访问码、会话是否重启/过期 | 重新登录，不把机器令牌填进访问码表单 |
| 403 | 网关日志、Origin/Host、当前角色 | operator 不能重试死信或读管理员状态；也可能是 CSRF 校验失败 |
| 502/503 或 backend unavailable | gateway 与目标服务日志，再看依赖 | 页面已到网关，下一跳或依赖没有就绪 |
| 实体暂无快照或过期 | `source-drone`/`drone_sim`、ingest、entity 日志 | 注册清单存在不等于状态已上报投影；检查模拟器是否结束 |
| 创建按钮没有可选实体 | 同步是否完成、实体是否新鲜、在岗、具备 inspect | 不能靠前端类型名称自行授予执行能力 |
| 任务一直等待 | task、dispatcher、对应 executor 日志和任务历史 | 检查任务事实与传输状态，不手动改成成功 |
| 搜索成功但没结果 | 确认筛选条件，查看 canal/search 日志 | 可能尚未同步；先确认任务详情中确实存在 |
| 搜索快照过期 | 页面“从第一页重新查询” | 旧签名游标不能重复使用，保留条件开启新查询 |
| 启动提示端口已占用 | 是否另一个目录或另一种模式还在运行 | 用原工程自己的停止入口处理，不杀无关进程 |

本地看日志示例：

```powershell
Get-Content './.local/logs/web.err.log' -Tail 80
Get-Content './.local/logs/task.err.log' -Tail 80 -Wait
```

任务问题提问时提供任务 ID、操作时间、页面错误码和相关服务日志；先检查并遮去秘密。完整排错知识库见 [troubleshooting](troubleshooting/README.md)。

## 8. 验收记录怎么看

页面能打开、单元测试通过、四类任务成功、完整 Docker 从干净卷启动、重启数据保留、真实浏览器布局/键盘操作，分别是不同验收项。新手操作步骤中的“应看到”是你本次需要核对的结果，不是本手册替你宣告本机已经通过。

本轮已实际运行 Docker 首次启动、真实浏览器登录/订阅/任务/搜索、四类任务与取消的 HTTP 验收、搜索重建及重启保留检查，证据见[全栈验收归档](verification/2026-09-12-fullstack/README.md)。你自己的电脑仍需实际执行上面的检查；旧课程记录与当前验证范围分开阅读。已有后端复核入口见 [复核资料](review/2026-09-12/acceptance.md)，教材复制检查见 [课程工作记录](learning/from-zero/BUILD-LEDGER.md)。

可使用[HTTP 全流程验收脚本说明](../scripts/test-fullstack.md)重复验证当前环境。Search 刚恢复时，gRPC 连接可能仍在退避重连；页面出现暂时不可用后，可稍后再次查询。部分错误提示保留英文诊断，处理方法见本文日志与搜索恢复步骤。
