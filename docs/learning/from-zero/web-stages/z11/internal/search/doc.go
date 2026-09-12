// Package search 构建任务事实的只读搜索投影；MySQL 始终是任务状态的权威来源。
//
// 数据流分成三个边界：snapshot 在一致读快照中导出任务；CDC handler 将
// Canal 的完整行转换为文档；Index 使用 status_version+1 作为 ES 外部版本，
// 防止重复与较旧事件覆盖新状态。不可处理的记录保持消费位点，交由维护恢复，
// 不能为消除 lag 而提交跳过。bootstrap 的 Complete、索引 UUID 与 CDCStart
// 共同证明本次索引可以启动，任何一个缺失都不能把半份数据当作完整搜索。
//
// 查询始终从可信 Principal 取租户，游标绑定租户、筛选条件和 PIT 到期时刻。
// 搜索命中有异步延迟；取消或重试前，调用者必须回到 TaskService 查询事实。
package search
