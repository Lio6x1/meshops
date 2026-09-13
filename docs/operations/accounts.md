# 个人账号、密码与会话

当前完整工程的网页登录使用个人账号。来源模拟器、执行器和服务间调用仍使用独立机器令牌。账号与实体数量是不同维度：一个操作员可以查看许多实体，一百万实体不等于一百万登录用户。

## 首次启动

按[完整启动手册](../run-fullstack.md)启动依赖和服务，再在项目根目录的 PowerShell 7 中执行：

```powershell
./scripts/account-admin.ps1 -Action Setup -Username admin
```

按提示两次输入自己设置的密码。密码为 8—128 个字符，最多 512 个 UTF-8 字节。命令通过标准输入把密码交给 Go 程序，不放进子进程参数，不写入凭证文件。账号数据保存在当前演示 MySQL 卷里。重复 Setup 不会重置已有管理员。

本机学习工程完成迁移、构建后，改用：

```powershell
./scripts/account-admin.ps1 -Mode Local -Action Setup -Username admin
```

这两种运行方式使用各自数据库，不要混淆。`-Action Status` 只查询是否已有管理员。旧演示访问码不再用于当前完整工程；历史课程的小阶段仍有自己的认证边界。

## 页面操作顺序

1. 用管理员用户名、密码登录，在“账号管理”创建操作员。管理员不能通过这个页面创建另一个管理员。
2. 操作员用初始密码登录后先改密。改密成功会退出，需要用新密码重新登录；完成之前不能进入实体、任务等业务页面，后端也拒绝业务请求。
3. 操作员能查看实体、创建和取消巡检任务、查询任务和搜索；管理账号、运行状态与死信人工重试属于管理员权限。
4. 账号停用或重置密码，会撤销该账号已有会话。重置后的操作员再次登录仍须改密。重新启用不会恢复旧 Cookie 或旧内部 RPC 令牌。
5. 普通用户通过“修改密码”更新自己的密码，必须提供当前密码。任务的 `createdBy` 记录具体个人账号 ID，不是共享的 operator 身份。

管理员忘记密码时，由拥有本机管理权限的人执行：

```powershell
./scripts/account-admin.ps1 -Action Reset -Username admin
```

这是显式恢复操作，会使管理员的旧会话失效。操作员忘记密码由管理员在页面重置；当前范围不提供邮箱、短信、公网自助注册和第三方登录。

## 后端如何实现

| 问题 | 当前方法 |
| --- | --- |
| 保存密码 | Argon2id，随机 16 字节盐，64 MiB 内存、3 次迭代、并行度 1、32 字节输出；存标准哈希串及参数，验证时从串中取盐重算 |
| 防止密码计算耗尽资源 | 限制输入长度、固定可接受的哈希参数、限制同时计算数量，并对登录入口限流 |
| 浏览器身份 | 随机不透明 Cookie，HttpOnly、SameSite；写请求还检查 Origin 与 CSRF |
| 内部 RPC 身份 | 网关持有另一个随机令牌；数据库只保存该高熵令牌的 SHA-256 查找键，RPC 拦截器解析个人账号 |
| 撤销 | 认证版本与有效期查库校验；修改凭据时撤销已有令牌；当前网关主动关闭对应 SSE，外部恢复操作由周期检查发现 |
| 并发管理 | 数据库事务锁定和校验账号；唯一约束防止重复用户名和重复初始管理员 |
| 审计 | 管理动作记录操作者、对象和动作；不把密码、哈希或会话秘密写进审计 |

SHA-256 在这里用于高熵随机令牌的索引，不用于密码哈希。浏览器 Cookie 和 RPC 令牌不是同一个值，也不把机器令牌发到前端。

## 验证与边界

账户测试覆盖哈希/盐、错误凭据、初始化重入、个人身份、首次改密、跨租户拒绝、权限边界、改密/停用/重置/注销撤销和并发写入。运行 `go test ./internal/accounts ./internal/web ./internal/platform ./cmd/account-admin`；连接真实依赖后，通过项目[集成测试入口](../../scripts/test.ps1)执行完整验收，不能把跳过的数据库测试当成通过。

### 运行 30 项真实 HTTP 账号检查

[test-accounts.py](../../scripts/test-accounts.py) 使用 Python 3 标准库，连接已经运行的本机 HTTP 网关，不负责启动 Docker、迁移或设置管理员。先完成上文对应 Docker/Local 模式的 Setup，确保管理员能正常登录且不处于待改密状态。它自行创建随机用户名的验收操作员，不需要你事先提供操作员账号；全业务任务验收则另见 [test-fullstack 说明](../../scripts/test-fullstack.md)。

在项目根目录打开一个用于验收的 PowerShell 7 终端，交互输入现有管理员用户名和密码。下面只把凭证临时放进当前进程环境供 Python 子进程读取，密码不出现在命令参数或命令历史中：

```powershell
$accountTestCredential = Get-Credential -Message '输入当前环境的管理员用户名和密码'
$accountTestEvidence = Join-Path 'results' ('accounts-' + [Guid]::NewGuid().ToString('N') + '.json')
try {
    $env:MESHOPS_WEB_ADMIN_USERNAME = $accountTestCredential.UserName
    $env:MESHOPS_WEB_ADMIN_PASSWORD = $accountTestCredential.GetNetworkCredential().Password
    python -B scripts/test-accounts.py --base-url http://127.0.0.1:18090 --output $accountTestEvidence
    if ($LASTEXITCODE -ne 0) { throw '账号 HTTP 验收未通过，请查看本次结果文件。' }
} finally {
    Remove-Item Env:MESHOPS_WEB_ADMIN_USERNAME, Env:MESHOPS_WEB_ADMIN_PASSWORD -ErrorAction SilentlyContinue
    if ($null -ne $accountTestCredential) { $accountTestCredential.Password.Dispose() }
    Remove-Variable accountTestCredential -ErrorAction SilentlyContinue
}
```

`--base-url` 需为网关允许的回环 HTTP(S) origin，并包含实际端口；默认网页演示为 `18090`。脚本拒绝远程地址、URL 内凭证、查询串、额外路径和重定向，并禁用系统代理。`--output` 必填，输出文件必须不存在，避免覆盖之前的证据。凭证在发起登录时会存在于进程内存中，因此不要打印环境、开启记录凭证的调试输出或把这段改为硬编码密码。

30 项检查覆盖管理员登录及角色伪造拒绝、创建与重复用户名、操作员首次登录与强制改密、改密 CSRF、旧 Cookie/密码失效、可信个人会话、业务放行、非管理员管理拒绝、停用/启用/重置后的撤销与再次改密，以及两种角色注销。每次运行结束尝试停用本次创建的操作员，账号和审计记录会保留；它不删除已有账号，不改管理员密码，也不清空业务数据。

单次请求最多 10 秒，监督进程对整个检查和清理设置约 180 秒硬上限。成功需退出码 0，结果 `passed: true`，30 个 `checks` 全部 `passed`，且 `cleanup: passed`。结果只记录白名单阶段、状态、清理状态、脱敏失败分类和耗时，不保存密码、Cookie、CSRF 或原始 HTTP 正文。超时写请求可能仍在服务端完成，因此 `cleanup: unconfirmed` 不能当作已清理；失败先保留结果并核对服务日志与本次验收账号，不反复运行掩盖失败。

### 当前实测状态

2026-09-13 本次 Windows 真实依赖集成运行有 216 个顶层测试 PASS、3 个辅助 SKIP，23 项必需集成门禁 PASS；账号版本归档中的 Windows 普通测试为 [168 PASS（unit-release）](../../verification/2026-09-13-accounts-scale/unit-release.summary.json)，先前 Linux 普通 race 为 162 PASS；两次普通运行各有 22 项 SKIP（20 项外部依赖门控、2 项 edge/state 崩溃子进程辅助入口），不能称为真实依赖 race 已通过。前端 29 项测试与构建、上述 30 项账号 HTTP、demo 数量/运动/模拟控制及全栈个人任务审计、取消和 CDC 检查均已 PASS。账号版本教程最终复制与构建已完成；百万容量试验因消费积压未通过；这些检查不能互相替代。

浏览器会话保存在网关内存中，网关重启后需要重新登录；账号与哈希在 MySQL 中持久化。当前单网关最多 256 个浏览器会话，每个账号限制有效内部会话数量。它们是资源保护边界，不代表高并发用户系统的容量承诺。本机 HTTP 演示没有传输加密，远程部署应配置 HTTPS。初始化和恢复工具需要数据库管理能力，不是普通网页用户接口。

以上账号检查保留其归档版本范围。后续消费优化最终保留 Kafka 批量提交，最新代码测试、1500/2000 events/s 实验及尚未验证的范围见[消费优化验收](../../verification/2026-09-13-consumer-optimization/README.md)；旧版 CI 不能代替新提交的云端验证。
