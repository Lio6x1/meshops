# 12｜机器 token 用 SHA-256：成立条件与排错边界

[返回导航](README.md) · 上一篇：[Git 与 CI](11-git-ci.md) · 连接问题：[01](01-connection-auth.md)

## 为什么这里不是“密码只做一次 SHA-256”

本项目 token 是机器凭证，课程脚本用密码学随机数生成器生成 **32 字节随机值**。注册表用 SHA-256 摘要查找对应可信身份，再对候选原 token 做常量时间比较。这个设计依赖高熵随机输入，不能照搬为用户自选密码的存储方案。

人的短密码可被枚举，需要另行设计专用密码哈希和认证流程；这里没有实现该能力。摘要也不是加密、授权角色或可逆凭证，不能拿摘要代替 bearer token 发给服务。

## 真实容易混淆的边界

出站 RPC 仍要持有原 token，因此本地 Credentials、进程内存和 `.local/secrets.json` 里仍存在秘密。摘要索引没有消除这些地方泄漏的风险，也不是凭证轮换系统。不要为了“只存 hash”而删掉出站原值，否则客户端无法按当前协议认证；若要修改协议，应作为独立设计处理。

可信 tenant/source/executor/role 从注册身份取得，不接受请求任意覆盖。正确 token 也可能没有操作权限；这对应 PermissionDenied，不能靠修改请求 tenant 或关闭拦截器解决。ID 约束是小写 ASCII 字母、数字、下划线和连字符，大写 ID 被拒绝不一定是身份加载失败。

## 安全诊断与测试

只检查配置文件/环境项是否存在、路径是否指向当前实例、对应角色是否正确，避免输出值：

```powershell
Test-Path .local/secrets.json
go test ./internal/platform -run 'TestAuthBoundary|TestInvalidConfigurationAndCredentialsFailWithoutLeaking' -count=1 -v
```

不要运行 `Get-Content .local/secrets.json` 后把结果发进问题单，也不要全量转储环境变量。环境加载入口见 [env.ps1](../../scripts/env.ps1)，身份实现见 [registry.go](../../internal/platform/registry.go)。加载失败应修正本机配置；错误身份测试只使用测试凭证，不修改演示实例的真实身份文件。

本轮 platform 包测试通过（2.645s）；上述测试名是可重复的定向入口，不把源码审查说成已完成攻击演练或凭证轮换验收。[既有连接与权限验证](../learning/from-zero/verification/2026-09-10-debugging.md)记录了错误 token 得到 Unauthenticated、角色不足得到 PermissionDenied 等真实调用。

日志保留服务、错误码、角色类别及必要的非秘密身份标识；不保留 token、authorization、完整 DSN，也不要把 token 摘要当作允许公开的日志字段。任务审计中的幂等键摘要是另一用途，见 [03](03-report-dispatch-intent.md)：它减少原始键重复暴露，但不是对低熵输入的保密承诺。
