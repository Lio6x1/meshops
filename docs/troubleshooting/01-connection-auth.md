# 先区分连接失败、认证失败和业务拒绝

[返回目录](README.md) · 下一篇：[RetryDLQ 死锁](02-retry-dlq-deadlock.md)

## 现象与第一步

同样是“命令失败”，失败位置可能完全不同。先记录退出码和 gRPC code，再查对应层；不要连续换 token 或重启所有服务。

| 结果 | 先检查 | 避免误判 |
| --- | --- | --- |
| 退出 2、缺少参数 | CLI 帮助、必需参数 | 请求可能尚未离开客户端 |
| 连接拒绝/超时 | 当前配置的 endpoint、进程、端口 | 服务未监听与密码错误不是一回事 |
| Unauthenticated | 是否加载当前学习目录配置、凭证是否匹配 | 长度够不代表凭证有效 |
| PermissionDenied | 当前角色、租户、source/executor/entity 绑定 | operator 不能调用 admin 管理方法 |
| InvalidArgument | ID 格式、JSON、时间、分页参数 | 重试不会修好同一无效参数 |
| FailedPrecondition | 当前状态、能力、新鲜度、投递事实 | 依赖在线不代表实体可执行 |
| Unavailable | 依赖可达性、服务端错误阶段、事务是否失败 | 不能直接认定为网络，也可能是数据库死锁 |

在已加载本机配置后，可对**配置中实际使用的端口**做只读检查：

```powershell
Test-NetConnection 127.0.0.1 -Port 13306
Test-NetConnection 127.0.0.1 -Port 19092
Test-NetConnection 127.0.0.1 -Port 16379
Get-Process | Where-Object ProcessName -Match 'entity|task|dispatcher|search'
```

这里的端口是默认课程依赖端口，不代表 RPC endpoint。RPC 应查自己的配置；不要打印所有环境变量。健康检查只能证明所检查的依赖状态，不能证明快照新鲜、消费授权正确或任务已停止。

## 实际案例与验证

[2026-09-10 日志实验](../learning/from-zero/verification/2026-09-10-debugging.md)通过真实 TCP/gRPC 得到：缺少 entity 参数退出 2；错误凭证返回 Unauthenticated；operator 调管理接口返回 PermissionDenied；`BAD ID!` 返回 InvalidArgument；恢复正确查询后退出 0。七项检查均满足预期，没有把凭证写入报告。

完整只读练习见[日志与排错课](../learning/from-zero/lessons/debugging.md)及[诊断脚本](../learning/from-zero/exercises/diagnose.ps1)。确认脚本针对自己的学习目录后再执行，不复用别的实例的 PID 或配置。

如果 Unavailable 同时伴随并发管理请求，请继续看[数据库死锁案例](02-retry-dlq-deadlock.md)。如果只在实体 stale/offline/busy 时创建失败，应刷新来源或等待真实可用状态，不要删掉 Task 的能力和新鲜度校验。任务取消请求与执行结束的区别见[回报授权](03-report-dispatch-intent.md)。

## 记录与边界

记录命令类别、退出码、gRPC code、目标服务/端口及时间；任务问题补 task_id/status_version。不要把错误响应与服务端日志当作同一种证据：客户端被拒绝不保证服务端一定写了逐请求日志。该历史实验没有重新覆盖所有任务停滞场景，也没有证明生产鉴权或 TLS 能力。
