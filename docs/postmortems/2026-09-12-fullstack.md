# 2026-09-12：前后端接入与 Docker 演示中的实际问题

本文记录本轮实际遇到的问题、判断依据、修复办法及验证范围。相关完整启动步骤见[前后端启动手册](../run-fullstack.md)。这里的本机 `.cache/fullstack/` 路径用于查找详细临时输出；关键现象和测试结果同时写入本文，避免只留下缓存引用。

## 1. HTTP 请求设置了上下文超时，慢请求体仍可能阻塞

**现象与原因。** 初版网关考虑了请求头超时、业务调用超时和 SSE，但缺少整个请求读取的传输超时。客户端可以声明较大的 `Content-Length`，只发送一部分 JSON，然后保持连接。处理函数阻塞在 `json.Decoder.Decode(r.Body)` 时，业务 `context` 的超时并不能自动中断底层 socket 的 `Body.Read`。

**如何复现。** [真实 TCP 回归测试](../../cmd/web-gateway/main_test.go)启动真实 `net/http.Server` 和本地 TCP 连接，声明 `Content-Length: 100`，但只发送未完成的 `{"role":`。修复前，测试无法在 socket 的两秒截止时间内读到拒绝响应。这个初始失败来自本轮工具执行记录，没有另外伪造一份“红灯日志文件”。测试文件保留了可复现条件：只有实际服务器配置了 `ReadTimeout`，测试才将其缩短至 100ms。

**解决办法。** [网关服务器配置](../../cmd/web-gateway/main.go)增加 `ReadTimeout: 10s`，保留 `ReadHeaderTimeout: 5s`、`IdleTimeout: 60s` 和请求头大小限制。收到不完整请求体后，读取会因传输截止时间退出，处理函数返回错误响应。SSE 需要长期保持连接，因此全局 `WriteTimeout` 仍不设置；[流式接口](../../internal/web/stream.go)为每次写入单独设置五秒截止时间。

**实际验证。** 本机 Windows 普通测试和 Linux race 输出均包含以下通过记录：

| 证据文件 | 测试 | 记录结果 |
| --- | --- | --- |
| `.cache/fullstack/go-unit.jsonl` | `TestSlowRequestBodyIsBounded` | PASS，0.10s |
| `.cache/fullstack/linux-race.jsonl` | `TestSlowRequestBodyIsBounded` | PASS，0.11s |

这证明真实 TCP 慢请求体被释放，并收到 HTTP 400；不表示测试模拟了全部恶意流量或测量了服务器抗压能力。

**预防。** 评审 HTTP 生命周期时分别检查请求头、请求体、业务调用、响应写入和空闲连接。仅使用 `httptest.ResponseRecorder` 或正常 JSON 请求，不能覆盖这个 socket 阻塞问题。

## 2. Docker 构建下载 Go 模块失败，已有 Docker 网络代理恢复构建

**现象。** 首次构建在 `go mod download` 阶段失败，原始诊断为：

```text
lookup goproxy.cn on 192.168.65.7:53: read udp ...: i/o timeout
process "/bin/sh -c go mod download" did not complete successfully: exit code: 1
```

原文保存在 `.cache/fullstack/docker-build.txt`。当时部分镜像层仍能下载，失败点是构建容器内这次 Go 模块请求的域名解析/网络路径；不能从这一条日志推导出“宿主机 DNS 一定错误”“Go 版本坏了”或所有网络请求均不可用。

**解决办法。** 本轮确认当前 Docker Desktop 环境已有可用的 `http.docker.internal:3128` 代理入口，将它作为 Docker 的预定义 `HTTP_PROXY`、`HTTPS_PROXY` build args 传给这一次构建。没有修改系统 DNS、全局 Git/Go 配置，没有关闭 TLS 证书验证或 Go 校验和校验。

[启动脚本](../../scripts/demo-stack.ps1)现提供显式 `-BuildProxy` 参数，并拒绝含用户名/密码的代理 URL。例如，在已确认这个入口适用于当前电脑时：

```powershell
./scripts/demo-stack.ps1 -Action Up -BuildProxy 'http://http.docker.internal:3128'
```

这个地址是本机当次已经存在的 Docker 环境入口，不是项目要求所有用户配置的固定公共代理。`GOPROXY` 选择模块源，`HTTP_PROXY`/`HTTPS_PROXY` 选择请求的网络出口，两者作用不同。构建代理参数不会自动传入业务运行容器。

**实际验证。** `.cache/fullstack/docker-build-proxy.txt` 记录代理方式构建成功；源码后续更新后的 `.cache/fullstack/docker-build-final.txt` 再次记录三类最终镜像构建成功：

```text
Image meshops-demo-canal:local Built
Image meshops-demo-web:local Built
Image meshops-demo-backend:local Built
```

**预防。** 先保存具体下载阶段、目标地址和错误，再选择局部网络配置。不要为了“能编译”将秘密写入 Dockerfile，或者把一次环境问题改成永久关闭证书验证。多个应用服务共用后端镜像，脚本只构建 `init`、`canal`、`web` 三个不同目标，避免重复发起相同镜像构建。

## 3. 旧 Canal 在 DDL 解析处停止推进，需要重建派生搜索状态

**现象与定位。** 本轮检查旧 `meshops-course` 环境时，Canal 日志出现 `MySqlStatementParser.parseCreate` 相关解析错误，增量链路停在旧 binlog 的 DDL 位置。这里具体是 Canal 的 binlog 解析阶段没有继续前进；仅检查 Search 进程是否存活、ES 是否可连接，无法证明新的任务变化已进入索引。

该错误来自本轮对旧容器日志的只读诊断；目前没有将它的完整原文另存为本文附属日志。不要把旧环境的错误归到新 `meshops-demo` 的首次启动，也不要通过将位点改为 latest 跳过它。

**解决办法及已执行结果。** 使用[原有搜索恢复脚本](../../scripts/rebuild-search.ps1)停止相关消费者、失效搜索标记，清理专属 Canal 旧状态，从 MySQL 任务事实重新建立完整索引和 binlog 起点，再恢复消费。`.cache/fullstack/local-search-rebuild.txt` 已记录：

- 旧 Canal 容器停止并移除，依赖恢复健康。
- `Task snapshot imported; saved Canal start position.`
- Canal 重新启动，Search 启动成功。

以上日志证明恢复操作、快照导入和进程重启成功；它本身没有包含重建后新任务的 CDC 查询结果，不能单凭这份文件写成“所有增量链路已验收”。最终端到端结果应与对应验收记录一起核对。

**补齐 Docker 演示恢复入口。** 新增命令：

```powershell
./scripts/demo-stack.ps1 -Action RebuildSearch
```

该操作固定作用于 `meshops-demo`。只停止 Search/Canal；任务事实服务继续运行。完整快照成功后，重建 Canal 容器并逐文件清理元数据目录中已核实的 `meta.dat`、`h2.mv.db`、`h2.trace.db`、`h2.lock.db`。真实旧卷中发现 H2 TSDB 文件，说明只清 `meta.dat` 不足以消除旧解析状态。未知文件、目录或符号链接均会阻止清理。

恢复不删除 MySQL 事实表、不删除 Kafka topic、不删除任何命名卷。失败后保持两服务停止，并再次写入未完成标记。对应源码见[恢复辅助命令](../../cmd/demo-init/recovery.go)，成功/失败编排由[无 Docker 的脚本测试](../../scripts/demo-stack.test.ps1)验证；Linux race 记录中，`TestSearchRebuildPublishesOnlyCompleteValidatedMarker` 和 `TestCanalResetRejectsSymlinkWithoutTouchingTarget` 已通过。

**预防。** 区分业务事实和可重建索引。出现消费缺口或不支持的事件时，保留错误和位点，进入明确维护流程；不自动跳过记录、不伪造 `complete=true`。恢复验收必须再创建一条新任务，确认新的 CDC 能进入搜索。

## 4. 运行进程不用 root 数据库账户，仍需检查它能读到哪些秘密文件

**发现的问题。** 初版虽然给普通 Go 服务配置了 DML 数据库账户，并过滤了进程环境变量，但完整 `secrets.json` 仍可被共同 UID 10001 读取。这样环境变量中没有 root 密码，并不等于进程拿不到 root 密码。这个问题通过权限设计审查确认，不能只靠检查启动参数发现。

**解决办法。** 完整凭证保留在同一专属卷内，派生出按实际 Linux 身份分配的文件：

| 文件 | 属主 UID:GID | 权限 | 内容/读取方 |
| --- | --- | --- | --- |
| `secrets.json` | `0:0` | `0600` | 完整引导凭证，仅 root 初始化和 Canal 包装进程读取 |
| `runtime-secrets.json` | `10001:10001` | `0440` | Registry 机器令牌、游标密钥、应用数据库密码；普通 Go 服务及网关组读取 |
| `web-codes.json` | `10002:10001` | `0400` | 仅两类浏览器访问码；网关 UID 10002 读取 |
| `mysql-root-password` | `999:999` | `0400` | MySQL 镜像内 UID 999 读取的 root 密码文件 |

网关在 Compose 中使用 `10002:10001`；其他普通 Go 服务保持 `10001:10001`。业务容器对凭证卷只读。普通 `exec` 从 runtime 子集读取，只有实际启动 `web-gateway` 时才另外读取浏览器码文件。再次初始化核对派生文件内容，内容不一致报错，不静默轮换凭证；原始完整文件的权限也会先被收紧。

**实际验证与剩余范围。** [权限与内容测试](../../cmd/demo-init/separation_test.go)验证子集内容、普通进程不依赖完整凭证、派生内容不一致时拒绝覆盖；[Linux 测试](../../cmd/demo-init/separation_linux_test.go)检查文件 mode，并在 root 测试进程下检查指定 UID/GID。`.cache/fullstack/linux-race.jsonl` 记录 `TestLinuxDerivedCredentialModesAndRootProvisionedOwners` 已 PASS。

后续已在真实 Docker 容器中确认：UID 10001 可读 runtime、不能读完整秘密/浏览器码/root 密码；UID 10002 可读 runtime 和浏览器码、不能读完整秘密/root 密码；MySQL UID 999 可读专属密码文件、不能读完整秘密。检查仅输出读取权限布尔结果，不输出秘密内容，结构化记录纳入[本轮归档](../verification/2026-09-12-fullstack/README.md)。

**预防及边界。** 检查秘密时同时检查文件权限、挂载权限、运行 UID、继承环境和输出日志。当前普通后端仍共享可信 Registry 的机器令牌，这是受控单机演示的边界，尚不是生产级的逐服务秘密隔离。拥有 Docker 管理权限的人可以控制容器和卷，这套文件权限也不用于对抗 Docker 管理员。

## 5. 容器全部 healthy，宿主网页端口却拒绝连接

**实际现象。** 新演示首次启动后，各容器健康检查通过，宿主访问 `127.0.0.1:18090` 却被拒绝。检查 web 容器发现，`HostConfig.PortBindings` 有预期的回环端口配置，但 `NetworkSettings.Ports` 没有对应的实际发布端口。当时 web 与其他后端一样，仅连接 `internal: true` 的网络。

**原因与处理。** 内部探针访问的是容器自己的 HTTP 服务，不能覆盖 Docker 到宿主的端口发布路径。按照 [Docker 官方网络说明](https://docs.docker.com/engine/network/#connecting-to-multiple-networks)中前端连接多个网络的设计，为 web 同时配置普通 `frontend` bridge 与 `private` 内部网络；其他服务继续仅连接 `private`。web 经普通 bridge 提供唯一的宿主回环端口，通过内部网络访问网关。数据库、Kafka、ES、gRPC 和网关均没有因此增加宿主映射。

```yaml
# 摘录：完整配置以 compose.demo.yml 为准。
services:
  web:
    ports: ["127.0.0.1:18090:8080"]
    networks: [frontend, private]
networks:
  frontend: {}
  private: {internal: true}
```

**实际验证。** `.cache/fullstack/docker-port-fix.txt` 记录新增前端网络并重建 web 容器。主任务随后实际确认宿主 HTTP 200，以及已发布的 `127.0.0.1:18090` 端口；这两项检查来自本轮工具输出，不能仅从该文件中的 `Healthy` 一行推出。

**预防。** 启动验收分为容器内部探针、宿主 HTTP 访问、页面业务操作三个层次。不能在 `docker compose up --wait` 成功后就宣布用户一定能打开网页。[启动脚本](../../scripts/demo-stack.ps1)现已在成功提示前验证宿主 `/healthz` 和 `/`，分别设置五秒超时并要求 HTTP 200；失败保留容器与数据卷并返回错误。[离线编排测试](../../scripts/demo-stack.test.ps1)先以“Up 未探测两个端点”失败，修复后通过正常访问、连接拒绝和网页非 200 三种情况，并断言失败不打印启动成功、不删除卷。手动检查也可执行：

```powershell
(Invoke-WebRequest 'http://127.0.0.1:18090/healthz' -UseBasicParsing -TimeoutSec 5).StatusCode
(Invoke-WebRequest 'http://127.0.0.1:18090/' -UseBasicParsing -TimeoutSec 5).StatusCode
```

两项都应为 200。后续真实浏览器已验证登录、SSE 同步结束、任务成功和关键词搜索；独立 HTTP 脚本也验证了四类执行方、取消、角色权限与 CDC 收敛。

## 本文证据范围

本轮源码测试、镜像构建成功、旧搜索恢复操作都有上述本地证据。`.cache/fullstack/docker-first-up.txt` 记录的首次服务健康及 `Demo is running` 曾遗漏宿主端口问题，必须与第 5 节修复记录一起阅读。修复后宿主 HTTP 可访问，也不能替代浏览器操作、UID 拒绝验证和重建后新 CDC 验证。最终完成状态以该提交对应的统一验收记录为准；本文不拿历史课程验收替代本轮完整前后端验收。
