// Package tasks 保存任务业务事实及其分发记录，供 Task、Dispatcher 和 Executor
// 三组 RPC 使用。cmd/task 与 cmd/dispatcher 只把角色交给 internal/app 装配；
// 本包的 Service、Dispatcher 接收已建立的数据库和 RPC 客户端，不自行拥有这些
// 共享连接的生命周期。阅读业务入口从 service.go 开始，投递入口从 dispatcher.go 开始。
//
// # 业务事实与传输记录
//
// CreateTask 先验证调用者、inspect 参数、注册能力和新鲜实体快照，再用租户内
// idempotency_key 约束重复创建。相同键与相同规范化请求返回同一任务，参数冲突
// 不能靠悄悄换键解决。任务状态、状态版本、审计记录与 outbox 在同一 MySQL 事务
// 中更新；外部 Kafka/RPC 调用不属于这个数据库事务，不能宣称跨系统原子提交。
//
// CancelTask 写入的是取消意图，cancel_requested 不等于 CANCELLED；执行器确认
// 或其他权威终态到来前，执行可能仍在进行。状态上报必须符合身份、执行绑定、
// dispatch 记录和状态版本，event_id 及请求指纹用于精确重试。重复回执先于终态
// 屏障判断；晚到的终态结果可以保留为审计事实，但不能覆盖已经确定的业务终态。
//
// 分发记录只是一次传输尝试。execution_key 在业务执行期间不变；command_id
// 区分 execute/cancel，dispatch_id 与 attempt 区分重发。传输超时或进入 DLQ
// 不自动证明任务失败。人工 RetryDLQ 仅管理员可调用，并再次核验最新死信轮次、
// 截止时间、取消意图及终态；成功表示新重试轮次已持久化，不表示执行成功。
//
// # 后台循环与退出
//
// Service.Run 分别运行 outbox 发布和到期扫描，使 Kafka 发布阻塞不延迟超时
// 屏障。每个任务只领取最早未发布 outbox，先提交领取租约，再发布并按 owner
// 标记；租约失效可能导致重复发布，不能越过前一条事实。Dispatcher 的发送 worker
// 同样先保存不可变投递意图再发送，网络调用后重读行状态，避免覆盖并发 ACK/终态。
//
// executor 长连接按租户和执行器唯一注册，队列受数量与字节限制。RPC 退出清理
// 注册；发送超时通过退出 RPC 取消底层传输。运行循环接收调用者 context，停止时
// 先取消并等待 worker，再由 app 关闭共享连接。依赖故障保留可重试事实，不能以
// 跳过记录、清空 outbox 或重建业务执行键作为恢复手段。
package tasks
