# JSON 字段别名绕过重复检查，错误又回显超长键名

[返回目录](README.md) · 下一篇：[inbox 恢复](07-inbox-recovery.md)

## 实际输入与根因

原 Go `encoding/json` 结构体解码会按大小写不敏感规则匹配字段。`DisallowUnknownFields` 因而没有拒绝 `DURATION_SECONDS`，后续重复键 map 又按精确字符串区分它与 `duration_seconds`：

```json
{"duration_seconds":1,"DURATION_SECONDS":2}
```

该输入原先被接受，并产生后写覆盖。`Note` 也被当成合法 note。另一个长未知字段输入让公开错误包含 13,063 字节，原因是直接把 decoder 的原始 unknown-field 错误返回客户端。

## 修复和验证

[ParseInspect](../../internal/tasks/domain.go)只接受精确的 `duration_seconds` 与 `note`，保留整数 1..60、note 字节数、重复键/null/尾随 JSON 检查；decoder 原始错误转成固定长度诊断。规范化结构和幂等请求摘要不改变。

```powershell
go test ./internal/tasks -run 'TestInspectValidation|TestInspectRejectsCaseAliasesAndBoundsErrors|TestCreateHashUsesDeadlinePresenceNotClock' -count=1 -v
```

红测试捕获三个被错误接受的大小写别名和超长错误；修复后普通 tasks 测试通过，2.233s，也包含在后续定向任务回归中。这里保证有界错误及精确 schema，不宣称对任意大小 JSON 输入做了零开销解析。

排查只保留字段名类别、输入字节数和错误码，不需要把实际 note 或大段 payload 放进日志。旧客户端若使用大小写别名，应改成协议中的精确键名，而不是恢复宽松解析。
