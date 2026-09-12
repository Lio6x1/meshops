# 阅读地图与基础补课

返回[完整课程入口](README.md)。阅读为当前练习服务，不要求从头看完文档或另上一整套付费课程。先看项目自己的规则，再读技术原理；外部示例不能覆盖本项目的字段、版本和部署约定。

## 官方资料：按主题读

以下入口于2026-09-06打开核对。Go依赖按仓库go.mod与go.sum锁定，Kafka文档选3.7以对应现有Compose的3.7.0；文档网站更新不自动授权升级依赖。

| 编号 | 官方入口 | 本项目只先读这些内容 | 使用课程 |
| --- | --- | --- | --- |
| R01 | [A Tour of Go](https://go.dev/tour/) | Structs、Methods and interfaces、Errors、Defer；到并发课再看Goroutines/Channels | Z01—Z04、Z06 |
| R02 | [gRPC Go Basics](https://grpc.io/docs/languages/go/basics/) | service定义、生成客户端、Unary/server streaming/bidirectional streaming的调用路径 | Z02、Z04、Z06、Z07 |
| R03 | [Protobuf Field Presence](https://protobuf.dev/programming-guides/field_presence/) | presence与默认值区别，optional标量与消息字段 | Z01、Z03—Z04、Z06 |
| R04 | [Go数据库事务](https://go.dev/doc/database/execute-transactions) | BeginTx、Tx内查询/写入、Commit/Rollback，避免事务中混用DB操作 | Z07—Z08 |
| R05 | [Kafka 3.7 Design](https://kafka.apache.org/37/design/design/) | The Producer、The Consumer/Consumer Position、Message Delivery Semantics；不先学完整集群运维 | Z05、Z07、Z09 |
| R06 | [Redis Lua Scripting](https://redis.io/docs/latest/develop/programmability/eval-intro/) | 脚本执行与原子性、参数传递、脚本缓存；限制长脚本 | Z05 |
| R07 | [bbolt官方仓库文档](https://github.com/etcd-io/bbolt) | Opening、Read-write transactions、Buckets、Iterating、Compaction | Z06—Z07 |
| R08 | [Prometheus Metric and Label Naming](https://prometheus.io/docs/practices/naming/) | 基础单位、指标命名、标签取值与高基数问题 | Z09 |
| R09 | [Go Diagnostics](https://go.dev/doc/diagnostics) | Profiling；CPU、heap、goroutine、block/mutex分别回答什么问题 | Z09 |
| R10 | [Go Add a Test](https://go.dev/doc/tutorial/add-a-test) | `_test.go`、Test函数、失败断言、go test运行结果 | Z02及每次新增核心测试 |

课卡提到的“Cxx/Oxx/Axx”分别来自[协议契约](../implementation/contracts.md)、[运行规格](../implementation/operations.md)、[业务验收矩阵](../implementation/acceptance.md)。原S/T/V计划提供具体接口与断言，不能只看外部库教程就开始改架构。

## 按需补课，不额外扩项目

补课在对应课前插入。已经能完成检查题就直接继续；不需要把B01—B06全部当作必修课程。练习可放ignored scratch或当前测试文件中的小用例，验证后保留有业务意义的测试，不往仓库添加第二套教程应用。

### B01 Go结构体、指针、方法与接口

触发：Z02—Z04无法解释结构体指针、方法接收者或接口。

练习：从当前阶段的仓库结构与接口出发，指出方法接收者和接口方法如何对应；再用一个局部含 map 的结构体例子观察值复制后的共享。例子由教师给完整代码，不要求寻找已删除的旧示例。读 R01 对应部分。

完成检查：能说明指针/值、map引用共享、方法签名满足接口这三件不同的事；能找到当前请求参数在哪里被读取。用户用自己的话讲清后回到原课，不要求背内存布局。

### B02 测试与可识别错误

触发：Z02/Z04会运行go test但看不懂断言，或把所有错误返回nil。

练习：为配置校验写两行表：缺Manifest失败、有效Manifest通过。故意移除必填检查，观察失败，再恢复。读R10，练习定位测试文件、测试名、expected/actual。

完成检查：能区分编译失败、用例失败、用例没发现和外部环境失败；知道`errors.Is`处理错误链、gRPC错误码在RPC边界映射，纯转换包无需依赖gRPC。

### B03 JSON、UTC与字段缺失

触发：Z03/Z04把0电量当作未上报，或把数值单位混用。

练习：比较`{}`和`{"battery_pct":0}`，要求分别得到缺测与合法0；把2500厘米转换为25米，把毫秒时间戳转UTC，再故意输入version=1.5验证拒绝。读R03。

完成检查：能说出optional的必要性、整组件缺失的意义和静态fixture为什么不直接用于当前时间演示。不得借补课改变原始格式。

### B04 goroutine、锁、channel与context

触发：Z06不清楚谁拥有订阅队列、何时退出、锁内能否发送。

练习：构造容量2的队列和停止读的接收者；再启动正常接收者。用context取消停读端，验证工作协程退出；画出同一map的读写保护与关闭责任。读R01并发部分。

完成检查：能指出阻塞点、共享数据保护、唯一关闭责任，知道无限开goroutine不是背压方案。正式课使用屏障控制时序，不依赖随机sleep。

### B05 SQL、唯一约束与事务

触发：Z07/Z08只会单条INSERT，不能解释多表一起成功或回滚。

练习：在隔离库中按现有任务/Outbox表写一次事务，第二次写入注入错误后两表均不新增；并发相同幂等键观察唯一约束。由AI提供数据库连接和清理专用测试库的接线，读R04。

完成检查：能解释BeginTx/Commit/Rollback、事务内使用Tx、唯一约束与先查再插竞态；不会用清空默认库解决重复数据。bbolt课迁移同一“必须共同提交”的思路，但不把两种数据库API混用。

### B06 序列、重试、幂等与分位数

触发：Z04/Z07/Z09分不清几类编号或把重试次数当成功次数。

练习一：按顺序处理“成功、失败、成功”，标出连续ACK停在哪里；列task_id、execution_key、command_id、dispatch_id、event_id分别是否随重试改变。

练习二：给出95次10ms与5次1000ms请求，描述平均值和尾延迟为何不同；不要口算后冒充具体监控直方图的精确P99。结合项目报告字段解释哪些失败请求未进入延迟样本。

完成检查：能区分连续前缀、重复结果、并发冲突与实际吞吐；性能结论需要成功/失败和覆盖率一起报告。无需先学习完整概率论或分布式共识算法。

## 如何搜索，不把学习变成漫游

每次记录“我要解决的当前问题→项目里的函数→查的概念→写出的反例→实现结果”。例如Z05搜索Lua原子比较更新，而不是先看全部Redis面试八股；Z07搜索transactional outbox发送标记窗口，而不是引入新的消息队列。

优先用上表官方入口或仓库锁定依赖的API文档。遇到版本差异先比对go.mod和当前函数签名，不为适配网上文章随意升级框架。AI负责把术语翻译成当前代码里的行为，不把“自己搜一下”当成教学完成。
