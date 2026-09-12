# 08｜生成结束时，上传失败被 context canceled 覆盖

[返回导航](README.md) · 上一篇：[inbox 恢复](07-inbox-recovery.md) · 下一篇：[投影恢复](09-projection-recovery.md)

## 现象

网关达到 count/duration 上限，准备正常退出；上传恰好返回 PermissionDenied 等明确失败，最终却显示成功退出。持久队列可能仍有未确认记录。检查生成数量不足以证明上传已完成，必须同时检查上传结果与 ACK 进度。

## 原因：两个位置都可能丢掉错误

第一处是 CLI 结束生成后虽然等待上传 goroutine，却丢弃它的返回值。修复后 join 保留非取消错误。独立审查继续追到第二处：`Upload` 从 `uploadStream` 获得具体失败后，先看 `ctx.Err()`；只要生成器同时取消，原错误就变成 `context.Canceled`，下游 join 再正确也无法还原。

修复顺序是先保留明确的 InvalidArgument、PermissionDenied、Unauthenticated、FailedPrecondition，再处理取消。Unavailable 等可重试传输错误在本地主动结束时仍返回取消，避免退出时重新连接。不能把所有错误都当成成功，也不能让正常取消启动无限重试。

## 可重复的安全验证

```powershell
go test ./internal/edge -run 'TestUploadPreservesFatalFailureDuringShutdown|TestGatewayJoinPreservesUploaderFailureAtGenerationEnd|TestContinuousUploadWaitsForGenerationAndReplaysAfterLoss' -count=1 -v
```

第一个测试运行真实 `Upload` 控制流，用受控 Ingest client 在建立流调用处停住；先取消，再释放指定错误，精确复现交错顺序，不靠 sleep 碰运气。四个明确错误都要保留；Unavailable/Canceled 对照组仍要尊重取消。CLI join 测试单独注入结果通道，只能覆盖第二层接收者，因此两层测试都需要。

连续上传回归另外通过本地真实 gRPC 验证：初始空队列等待新数据、再次变空后继续等待、后续生成恢复上传、ACK 丢失后从持久确认位置准确重放。空队列不是流结束，不能提前 half-close。

## 实际证据与排错顺序

修复前四个明确错误分支都得到 `context canceled`；修复后上述定向组三项通过（4.080s）。这不是实际网络认证故障演练，也不是全量最终验收。

遇到类似现象，保留结束原因、上传最终 gRPC code、已生成/待确认数量、最后持久 ACK、重连次数；先判断是正常停止还是明确拒绝，再检查身份与连接。不要记录 bearer token、完整请求或原始队列数据。持久队列里的未确认记录应按既定重放规则恢复，不能为消除 pending 数量而清空文件。

实现与回归：[uplink.go](../../internal/edge/uplink.go)、[cli.go](../../internal/edge/cli.go)、[upload_review_test.go](../../internal/edge/upload_review_test.go)。
