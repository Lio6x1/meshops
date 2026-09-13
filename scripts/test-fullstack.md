# 本机 HTTP 全流程验收

`test-fullstack.ps1` 通过正式 HTTP 网关验证已经启动的工程，不启动 Docker，不清空数据，不替代浏览器视觉验收。先按 [完整启动手册](../docs/run-fullstack.md) 启动 Docker 模式或 IDE 模式，确保六个来源模拟器、四个执行器和搜索服务均在运行。

使用 PowerShell 7。先初始化管理员，在账号管理页面创建操作员，并以该操作员登录完成首次改密。下面交互输入这两个账号的密码，不要把密码写进命令历史、脚本或提交到 Git。

```powershell
$operatorCredential = Get-Credential -Message '输入操作员用户名和密码'
$adminCredential = Get-Credential -Message '输入管理员用户名和密码'
try {
    ./scripts/test-fullstack.ps1 `
        -BaseURL 'http://127.0.0.1:18090' `
        -OperatorUsername $operatorCredential.UserName `
        -OperatorPassword $operatorCredential.GetNetworkCredential().Password `
        -AdminUsername $adminCredential.UserName `
        -AdminPassword $adminCredential.GetNetworkCredential().Password
} finally {
    Remove-Variable operatorCredential, adminCredential
}
```

`BaseURL` 必须是当前网关允许的本机 HTTP(S) 地址；默认 `http://127.0.0.1:18090`。脚本拒绝远程地址、用户信息、额外路径、查询串和片段，并禁止自动跟随重定向。密码在发送登录请求时必然需要转成内存中的字符串；上述写法用于避免明文落入命令历史，不代表进程内存完全不含明文。

默认输出 `results/fullstack-<本次运行ID>.json`。可通过 `-OutputPath 'results/my-http-acceptance.json'` 指定新的证据文件；已存在的文件会被拒绝覆盖。结果只保存实体标识、任务标识、版本、状态历史、分发标识和检查结论，不保存登录响应、Cookie、CSRF、密码、原始错误正文或完整业务响应。

每次运行会留下五个正常业务任务：人员、无人机、车辆、机器人各一个 1 秒巡检，以及一个进入 `EXECUTING` 后取消的 30 秒无人机巡检。每次使用新的幂等键和检索词，因此可重复运行，不依赖空数据库。四个成功任务会用相同请求体重复创建一次，要求返回相同任务 ID，并检查最终 `effect_count=1`。这验证的是当前模拟执行器的效果记录，不等同于真实硬件端到端 exactly-once 保证。请避免在验收期间手动取消这些任务或停止模拟器。

检查还包括：六类实体的新鲜快照；四个成功任务及取消任务的事实、历史和分发身份一致；搜索 CDC 最终收敛到相同状态及版本；operator 读取管理状态返回 HTTP 403 / PermissionDenied，admin 返回真实状态；两个会话注销后均返回 HTTP 401。HTTP 单次最多等待 10 秒，轮询分别有超时，整体最多约 10 分钟加当前请求结束时间。

成功时会显示各检查的 `PASS`，最终 JSON 的 `passed` 为 `true`。失败时脚本抛出错误，JSON 记录阶段和脱敏原因；先按启动手册检查该阶段对应服务的日志，再重新运行并生成新的证据文件。失败不会自动回滚已经创建的业务任务。

只验证本地参数拒绝逻辑、完全不发送 HTTP：

```powershell
./scripts/test-fullstack.parameters.test.ps1
```

脚本不是预先生成的验收结论。只有对当前运行环境实际执行并得到 `passed: true`，才能证明本次 HTTP 验收成功；页面布局、SSE 浏览器体验、容器首次启动和故障恢复还需各自的验收记录。
