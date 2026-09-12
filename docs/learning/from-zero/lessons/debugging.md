# 日志与排错｜从错误现象找到具体环节

这不是等项目写完才看的附录。Z02开始学习区分客户端和服务端；Z04结合鉴权与输入验证；Z07装配完成后执行本页完整实验。遇到错误先保留证据、定位原因，再修改并复验，不靠不断重启碰运气。

## 1. 项目已经有什么日志

业务代码使用Go标准库 `log/slog`，不需要额外安装。当前没有统一配置JSON handler，业务slog默认输出文本，框架输出与业务日志可能格式不同。opctl的错误JSON是客户端自己编码的响应诊断，不代表所有服务日志都是JSON。

真实启动日志示例（时间随运行变化）：

```text
2026/09/10 15:12:02 INFO service started role=entity rpc=127.0.0.1:50052 metrics=127.0.0.1:18081
```

依次读时间、INFO级别、消息、role、RPC地址和指标地址。消息描述发生了什么，字段给出定位条件。INFO是正常事件，WARN表示需要关注的情况，ERROR表示错误；这些级别不会自动让程序退出，也不会自动执行重试，业务代码决定下一步行为。

启动脚本把每个进程的stdout和stderr分别写进 `.local/logs/<进程名>.out.log` 和 `.err.log`。slog默认写stderr，因此err.log包含INFO是正常的。控制台直接启动服务时，日志显示在该终端，不能期待start脚本自动替你收集。

```powershell
Set-Location 'D:\job\golang\projects\meshops-course-lab'
Get-ChildItem ./.local/logs
Get-Content ./.local/logs/task.err.log -Tail 50 -Wait
```

`-Tail 50`只读最后50行，`-Wait`等待新内容；Ctrl+C停止查看，不会停止Task服务。网关日志按personnel_sim、drone_sim等命名，执行方按simulated_aircraft等命名，而非统一叫executor.log。

## 2. 先判断错误发生在哪一层

| 现象 | 先检查哪里 | 为什么 |
| --- | --- | --- |
| `--entity required`，退出2 | opctl参数 | 请求还没发出，不应该先查数据库 |
| `Unauthenticated` | token环境是否加载、凭证是否属于当前环境 | 身份没有通过认证 |
| `PermissionDenied` | 调用者角色与实体/执行方绑定 | 身份有效不代表有权调用 |
| `InvalidArgument` | 请求字段、版本、格式 | 合法身份也可能发送非法数据 |
| `Unavailable`或`DeadlineExceeded` | 地址、进程、依赖及超时 | 单凭错误码不能断言是数据库坏了 |
| 正常JSON但found为false/省略 | 数据是否已上报、租户和实体ID | 查询成功但不存在，与RPC失败不同 |
| 任务长期停在某状态 | Task事实、分发记录、对应服务日志 | 创建成功不等于已经执行 |

退出码要在命令后立即保存，下一条原生命令可能覆盖 `$LASTEXITCODE`。opctl的约定是：0为命令成功，1为运行/RPC错误，2为用法错误。PowerShell脚本自身抛出的异常还要看异常信息，不能只套这三个数字。

## 3. 准备完整实验

前置是Z07-05或之后的学习工程，已经执行initialize。服务没有运行时执行下面命令；已运行则跳过start，避免重复占用端口：

```powershell
Set-Location 'D:\job\golang\projects\meshops-course-lab'
./scripts/start.ps1
. ./scripts/env.ps1
./bin/opctl.exe snapshot --entity drone-001
```

这里不要求启动模拟器：found=false也能证明查询请求成功。若正确查询都失败，先解决环境问题，不继续把连接失败误判为后面预期的权限错误。每个新终端都要进入该学习目录并载入自己的env；不要展示或复制secrets.json到日志报告。

以下每个实验都按“触发→查看→定位→恢复”执行。是修改请求或运行环境，不需要故意破坏已有正确业务代码。

## 4. 实验一：漏参数，服务端为什么没有日志

```powershell
./bin/opctl.exe snapshot
$commandExit = $LASTEXITCODE
Write-Output "exit=$commandExit"
```

预期stderr输出 `--entity required`，退出码2。定位到 [opctl.go](../../../../internal/cli/opctl.go) 的参数检查：snapshot要求entity非空，失败调用usage并返回2。此时还没有建立RPC，所以Entity没有收到请求，也不一定新增日志。

恢复：

```powershell
./bin/opctl.exe snapshot --entity drone-001
if ($LASTEXITCODE -ne 0) { throw 'corrected query still failed' }
```

不要通过去掉“entity必填”的校验来消除错误；正确修复是补齐调用参数。只有你正在实现的新代码错误地拒绝了合法输入时，才应修改校验规则并补回归测试。

## 5. 实验二：错误凭证与正确凭证

用单独的临时变量，不覆盖真实操作员token：

```powershell
$previousDebugToken = [Environment]::GetEnvironmentVariable('MESHOPS_DEBUG_BAD_TOKEN','Process')
try {
    $env:MESHOPS_DEBUG_BAD_TOKEN = 'invalid-debug-credential-000000000000000000000000'
    ./bin/opctl.exe snapshot --entity drone-001 --token-env MESHOPS_DEBUG_BAD_TOKEN
    $commandExit = $LASTEXITCODE
    Write-Output "exit=$commandExit"
} finally {
    [Environment]::SetEnvironmentVariable('MESHOPS_DEBUG_BAD_TOKEN',$previousDebugToken,'Process')
}
```

预期退出1，错误JSON的code是Unauthenticated，message包含 `invalid credential`。测试字符串故意足够长，越过客户端的“凭证长度至少32”检查，真实RPC到达服务端后才被拒绝。若message是 `credential environment is missing or too short`，则错误发生在客户端，两者不是同一条路径。

定位到 [auth.go](../../../../internal/platform/auth.go) 的authorize：读取Bearer → Registry.Authenticate → 失败返回Unauthenticated。它不会因为使用slog就自动为每个认证失败写一条业务日志；错误响应本身也是证据。

恢复时重新使用默认操作员环境变量：

```powershell
./bin/opctl.exe snapshot --entity drone-001
if ($LASTEXITCODE -ne 0) { throw 'correct credential still failed' }
```

若仍失败，检查是否在另一个学习目录生成了新凭证，却连接旧目录的服务。不要通过把token内容打印出来比对，先核对运行目录、端口和加载的env文件。

## 6. 实验三：身份合法但角色不够

```powershell
./bin/opctl.exe dispatcher status --token-env MESHOPS_OPERATOR_TOKEN
$commandExit = $LASTEXITCODE
Write-Output "exit=$commandExit"
```

预期退出1，code是PermissionDenied，message包含 `role cannot call method`。操作员允许GetDispatch，但没有管理端GetStatus权限。定位到auth.go的permitted，看operator和admin两个分支，不能把所有权限问题都解释成token错误。

恢复时显式使用本地课程管理员身份：

```powershell
./bin/opctl.exe dispatcher status --token-env MESHOPS_ADMIN_TOKEN
if ($LASTEXITCODE -ne 0) { throw 'admin status query failed' }
```

这只是演示角色边界，不是让所有业务一律改用管理员。正常查看某个任务的分发记录仍使用操作员GetDispatch。

## 7. 实验四：非法编号与查询不存在

```powershell
./bin/opctl.exe snapshot --entity 'BAD ID!'
$commandExit = $LASTEXITCODE
Write-Output "exit=$commandExit"
```

预期退出1、InvalidArgument、`invalid entity ID`。这里非空参数越过了CLI必填检查，但服务端要求编号符合格式，所以仍会拒绝。

恢复为合法格式：

```powershell
./bin/opctl.exe snapshot --entity drone-001
if ($LASTEXITCODE -ne 0) { throw 'valid entity query failed' }
```

如果found=false，说明合法编号当前没有可返回状态，不等于编号格式非法。后续启动网关上报，再检查Entity消费进度；不能把所有“查不到”都改成返回一份默认无人机数据。

## 8. 一次跑完并保存证据

完整参考脚本：[exercises/diagnose.ps1](../exercises/diagnose.ps1)。它只查询已经运行的服务，不创建任务，不停容器，不清队列；你可以打开文件看全部实现，也可以直接运行：

```powershell
Set-Location 'D:\job\golang\projects\meshops-course-lab'
. ./scripts/env.ps1
& 'D:\job\golang\projects\meshops\docs\learning\from-zero\exercises\diagnose.ps1' -Project (Get-Location).Path
```

预期7条PASS：初始查询、4种错误、正确查询、管理员查询。每次结果单独写入 `.local/debug-exercises/<本次编号>/`，包含stdout、stderr和result.json。任何实际错误码不符合预期，脚本都会失败；不是出现任意错误就算完成实验。

读脚本时按以下顺序：

1. Resolve-Path确认目标工程，检查opctl和所需环境变量；这一步不自动初始化数据库。
2. 创建本次独立输出目录，避免上一轮结果冒充本轮。
3. Invoke-DebugCase调用真实opctl，将stdout和stderr分别重定向，立即记录退出码。
4. 对预期RPC错误解析JSON并比较code；用法错误是纯文本，单独检查，不硬把它解析为JSON。
5. 成功请求要求返回可解析的JSON；这里不强制found=true，因为此实验验证调用链，不验证模拟器已上报。
6. finally恢复临时环境变量、工作目录并写本轮结果，失败也保留证据。

实验结束，如果服务是你本次启动的，执行 `./scripts/stop.ps1`。当前脚本没有日志轮转或集中采集；在重启程序前，把需要保留的日志复制到单独的排错目录，不能假设同名日志文件永久保留所有运行历史。

## 9. 任务卡住时，沿实际链路查

手动实验前先用 `./scripts/start.ps1 -Simulators` 启动完整演示环境；已运行的普通服务需要先stop再按模拟器方式启动。下面创建一个明确的本地inspect任务，key每次新建；观察重试同一请求时则必须复用原key：

```powershell
. ./scripts/env.ps1
$debugKey = 'debug-' + [guid]::NewGuid().ToString('N')
$created = ./bin/opctl.exe task create --entity drone-001 --key $debugKey --duration-seconds 1 | ConvertFrom-Json
if ($LASTEXITCODE -ne 0) { throw 'task creation failed; inspect its error first' }
./bin/opctl.exe task get --id $created.taskId
./bin/opctl.exe task history --id $created.taskId
./bin/opctl.exe dispatch get --task $created.taskId
```

刚创建时尚未完成是正常异步过程。结合状态和日志定位：

| 观察 | 下一步查什么 |
| --- | --- |
| 创建本身被拒绝 | 错误码、目标快照新鲜度、可信inspect能力与执行方绑定 |
| 长期DISPATCH_PENDING且没有分发记录 | Task的Outbox日志、Kafka发布及Dispatcher消费 |
| 有分发尝试但没有ACKED | Dispatcher日志、执行方连接与本地inbox错误 |
| ACKED或EXECUTING后不前进 | 执行方日志、待报状态、Task回报拒绝原因 |
| 进入DLQ | 本轮尝试的错误及任务当前事实，先恢复原因再考虑管理员重试 |

过滤本次任务相关日志可以执行：

```powershell
Select-String -Path ./.local/logs/task.err.log,./.local/logs/dispatcher.err.log,./.local/logs/simulated_aircraft.err.log -SimpleMatch $created.taskId
Get-Content ./.local/logs/task.err.log -Tail 80
Get-Content ./.local/logs/dispatcher.err.log -Tail 80
Get-Content ./.local/logs/simulated_aircraft.err.log -Tail 80
```

当前并非每条日志都带task_id，过滤为空不能证明服务没处理。Task事实、审计与dispatch记录用于补齐证据，不靠搜索一个ID替代全部排查。

实际Outbox日志代码：

```go
slog.Warn("outbox publication retained for retry", "outbox_id", id, "tenant", tenant)
```

第一个参数是消息，后面按键、值配对。outbox_id帮助定位发布记录，tenant提供租户范围。它说明记录仍保留以便重试，不说明重试已成功；还要重新查询任务进展。源码有意不打印全部payload或凭证，所以有些日志只够定位环节，具体原因还要结合依赖状态和调用错误。

Kafka消费日志中的topic、partition、offset表示卡住的消息位置。持续出现同一位置，说明handler重试还没成功；不能修改成“出错就提交offset”来让日志消失，那会跳过未处理的数据。

## 10. 找到问题后，怎样确认真的修好了

如果是参数、凭证或地址错误，恢复正确设置后重做原操作，并确认状态恢复；不改业务代码。若是你编写的实现有bug，先用原输入得到稳定失败，再增加能复现它的测试，修改最小相关部分，最后验证正确输入和错误输入都符合约定。

例如修改ACK逻辑后，在当前完整学习工程运行：

```powershell
go test ./internal/state -run '^TestWholeBatchAndPrefixACK$' -count=1 -v
if ($LASTEXITCODE -ne 0) { throw 'ACK regression failed' }
```

修改inbox重复执行逻辑后运行：

```powershell
go test ./internal/edge -run '^TestInboxEffectAndReportSurviveRestart$' -count=1 -v
if ($LASTEXITCODE -ne 0) { throw 'inbox regression failed' }
```

前者使用发布替身固定失败位置，后者使用真实临时bbolt文件；都不能代替真实MySQL/Kafka集成验证。涉及事务、发布、分发或跨服务回报时，在隔离课程环境按 `./scripts/test.ps1 -Integration` 执行相应集成验收，再复跑原操作。纯讲义文字变化无需重新压测整个系统。

建议每次留下四项记录：触发命令与时间、实际错误/状态、定位到的函数及原因、修复后的测试和业务结果。既不要只写“已经解决”，也不要用大量无关日志淹没有效证据。

## 当前边界

当前提供基础业务日志、错误码、任务审计和指标入口，尚未统一全部日志字段、输出格式、日志轮转及跨服务关联ID。`slog.WarnContext(ctx, ...)`本身不会自动把task_id或trace_id写进去。课程暂不引入ELK等额外平台；先学会用已有证据定位，后续确有需要再补统一关联能力。

本页四种只读错误实验已在真实Entity/Dispatcher服务验证，详情见 [排错验证记录](../verification/2026-09-10-debugging.md)。任务停滞表是诊断指导，不声称本轮重新制造了每一种任务故障。
