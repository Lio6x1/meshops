// Package bus 封装 Kafka 的持久化边界与消费恢复规则，不负责理解任务或实体业务。
// New 创建一个可跨 topic 使用的同步 writer，调用者最终负责 Close；每次 Consume
// 自己创建并关闭 consumer group，每个分区 worker 创建并关闭对应 reader。
// 调用者应先取消并等待消费循环，再关闭共享发布资源。
//
// # 发布和逐分区消费
//
// Publish 复制消息字节，并等待 broker 的 RequireAll 确认；没有确认不能返回
// “已发布”。调用受调用者 context 和内部操作超时共同约束。EnsureTopics 校验
// 课程约定的 topic/分区布局，Ping 与 Lag 通过真实 broker 查询，不用常数代替
// 不可用的依赖状态。
//
// 消费组每个已分配分区只有一个串行 worker，预取队列有界。handler 成功以后
// 才提交该记录的下一 offset；业务处理失败时持有同一条记录重试，后面的 offset
// 不得越过失败点。一个分区失败不阻止其他分区处理；记录的 topic/partition/offset
// 通过 RecordMetadata 附在 context，供业务错误诊断和恢复证据使用。
//
// handler 已成功但 offset 提交失败，或者进程在两者之间退出，都会造成重复处理；
// 所以本包提供的是至少一次交付边界，业务持久化必须自行具备幂等性。提交阶段
// 遇到 generation 失效时结束本轮 worker，不能以重试旧租约证明仍拥有分区。
// handler 必须遵守取消，因为再平衡需要等旧 generation 的 worker 释放资源。
//
// # 保留期缺口与错误恢复
//
// Consume 是明确允许有损保留后缀恢复的入口：缺口会记录日志与计数，不能称为
// 无损补齐。ConsumeStrict/ConsumeStrictFrom/ConsumeStrictPartitions 面向完整
// 投影；期望 offset 已被保留期删除时返回 RetentionGap。逐分区起点必须来自调用方
// 已验证的完整快照边界，未列出的分区不能获得跳过历史的权限。
//
// 网络、选主和暂时存储错误保持进度并重试；明确的身份、权限或无效 topic 拒绝
// 会终止消费并交还调用者处理。日志区分读取、业务应用、提交等阶段，并报告恢复，
// 不把任意错误文本中的载荷或凭据复制到日志。取消是资源回收信号，不是跳过记录
// 的授权；本包不会在错误恢复中清库、删除 topic 或伪造消费成功。
package bus
