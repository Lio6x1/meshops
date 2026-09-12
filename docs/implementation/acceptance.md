# 验收矩阵与演示流程

状态：本页保留原 A01—A28 核心验收规格，日期结果见 [关键验收记录](../../verification/2026-09-10/summary.md)。完整实现已迁入仓库根目录；搜索与前后端新增范围另行验收。表中预期必须由测试证明，逐课复制验证和学员学习情况单独记录。

## 1. 测试环境与证据规则

纯函数测试使用固定Clock=2026-09-05T00:00:01Z和原始fixtures；集成测试发生时间由当前测试Clock生成，避免30秒过期或7天历史过滤误伤静态样例。每个integration运行用独立MySQL数据库、Redis实例或DB/视图前缀、Kafka topic/group前缀及临时bbolt目录。测试不得清空默认meshops数据库或用户已有数据卷。

四服务配置须允许测试指定topic/group前缀（MeshOps.TopicPrefix/ConsumerGroupPrefix，默认空）；生产示例固定主题名。测试为六类型×实际事件数生成输入清单，包含tenant/source/entity/version/event_id/operation，作为恢复与去重对照。测试运行前记录依赖版本及有效非敏感配置；密钥用环境变量名替代。

证据目录建议 `docs/results/<run-id>/`，写summary.md、commands.txt、config.redacted.yaml、results.ndjson和必要的小型日志。大体积pprof/原始日志放ignored的benchmark-results并由CI上传artifact，summary记录artifact位置与摘要；不得把机器绝对路径当成其他读者可获取的证据。

## 2. 必选验收矩阵

| ID | 场景与操作 | 必须看到的结果 | 任务 |
| --- | --- | --- | --- |
| A01 | 缺配置、缺token、同token绑定两身份、非法容量 | 启动/加载失败，指出字段，不泄露token | S01 |
| A02 | source伪造其他来源、operator冒充executor、双租户相同entity_id | 认证/授权拒绝；同ID两租户快照和任务互不泄露；Unary/Stream均覆盖 | S01/S03/S04/T01/T03 |
| A03 | 空库001→003、有旧task/Outbox的002→003、重复seed与冲突seed | 结构可用、旧执行键/待发事实保留、相同seed幂等、冲突失败；旧重复审计不被静默删除 | S01 |
| A04 | person/drone样例及缺字段、负版本、小数版本、越界经纬度、未知字段、零电量 | 单位/UTC/组件正确；非法输入有明确错误；0电量合法；人员无Power | S02 |
| A05 | Kafka不可用、整批混入非法项、Producer结果[成功,失败,成功] | 不返回整批成功；非法整批不写Kafka；仅ACK连续前缀1 | S03 |
| A06 | ACK丢失、CloseSend、同source双流、错误epoch/越界ACK | 事件可重复、ACK不越缺口；最终ACK处理正确；冲突流拒绝，客户端不误删 | S03 |
| A07 | v2→v1→v2重复→同版本不同内容；写Redis后提交offset前崩溃 | 最终v2原内容；重复无破坏、冲突有记录、重启可继续 | S04 |
| A08 | DELETE v3后重放旧事件、过期与不存在、Redis不可用/OOM | 不复活；found与stale可区分；依赖错误返回UNAVAILABLE且位点不越过失败 | S04 |
| A09 | 六类fixtures全部走真实上报→快照 | 六类专有字段可查询；vehicle36km/h=10m/s、robot0.65=65%；sensor0不丢；facility容量约束生效 | S05 |
| A10 | 注册订阅后、读取快照期间并发更新；不存在ID、删除、重连 | SNAPSHOT_END存在，最终版本正确；新sync替换旧视图，不残留已删除对象 | S06 |
| A11 | 一个慢订阅者、一个正常订阅者；控制队列字节/条数、取消ctx | 正常端继续；慢端合并或明确断开；队列/字节有界，10秒内释放资源 | S06 |
| A12 | 网关offline生成100条→退出→同DBdrain；ACK落盘前强杀 | 重启epoch不变、版本连续、最终pending0，输入未确认事件都能追踪 | S07 |
| A13 | 缓存容量满、损坏序列、离线压缩 | 不覆盖未ACK事件；损坏保留原库报错；压缩前后水位/事件/版本相同 | S07 |
| A14 | 重复抽样、跨页相同时间戳、游标签名/租户错误、旧事件/预算超限 | 同事件至多一份样本；分页不重不漏；非法游标拒绝；年龄与预算行为可观测 | S08 |
| A15 | 20并发相同创建键、同键不同payload、无deadline重试 | 1任务/1创建Outbox；原task_id一致；不同payload ALREADY_EXISTS | T01 |
| A16 | MySQL事务中途失败、两个回报同expected版本 | task/history/outbox/reports不部分提交；最多一条合法推进，冲突ABORTED | T01 |
| A17 | Get/List/History、跨租户、重放同报告 | 查询真实结果且审计有序；无越权；同报告DUPLICATE不增加版本/Outbox | T01 |
| A18 | Outbox发送后标记前崩溃、Kafka停机、同task连续事件 | 稳定event_id重发、积压可恢复；同task发布不倒序；>7天未发不排除 | T02 |
| A19 | 分发落库后提交offset前崩溃、执行方断线、错executor监听 | command/attempt不重复创建；任务仍pending可重发；越权流收不到命令 | T02 |
| A20 | 前四类各1个inspect；对sensor/facility创建inspect | 前四类完成且GetTask可读result；后两类FAILED_PRECONDITION且无task/Outbox | T03 |
| A21 | 同command两次；结果落盘后回报前强杀；回报响应丢失 | effect_count=1；重启回报原结果；相同event_id不重复推进状态 | T03 |
| A22 | cancel先于execute；完成/取消确认/timeout屏障并发；超时后成功回报 | 取消墓碑阻止执行；首个合法终态唯一；迟到结果入审计不覆盖终态 | T04 |
| A23 | 5次投递失败、Kafka DLQ通知失败、并发RetryDLQ、终态任务重投 | MySQL有dlq事实；可恢复通知；同旧dlq只1个新轮次；终态/过期拒绝重投 | T04 |
| A24 | 按本页全流程用CLI运行；缺参数/错误凭证/取消未完成 | JSON与退出码正确；命令有真实RPC效果；不打印虚假完成 | V01 |
| A25 | 已知输入清单、停生成/旧Entity、Redis隔离清空、重建并重连 | 新viewGeneration，所有已知最新版本/墓碑正确；影子未完成不切换 | V02 |
| A26 | 故意删除Kafka窗口外冷实体事实；分别停Kafka/Redis/MySQL/服务进程 | 明确无法完整恢复或降级；不冒充完整恢复、不无限回源/重试；失败窗口留证据 | V02 |
| A27 | 100events/s基线与至少一档更高负载，10000个不同实体ID | 真实吞吐/错误/P99/Lag/内存报告；指标基数不随实体ID线性增长 | V03 |
| A28 | 新读者按README从初始化到演示，再运行必选用例 | 所有必选任务有可定位证据；无空跑当成功，无未说明跳过；六类都可展示 | V03 |

集成断言检查真实持久状态和最终业务结果。不得用“日志出现了发送成功”证明任务执行一次，也不得用业务服务器自己生成的期望值证明投影正确。

A02分阶段记录：S01验证身份解析和Unary/Stream角色拦截；S03/S04/T01/T03补齐真实资源隔离。T01的A16/A17可先通过真实MySQL repository测试验证事务和去重，使用明确标记的已校验dispatch输入；跨服务回报鉴权须T02/T03接通后补验。子项通过不能把整项提前标为完成。

## 3. 当前演示与验收入口

从 [根 README](../../README.md) 执行现行 PowerShell 初始化、启动、demo 和测试命令；网页模式使用 [完整前后端手册](../run-fullstack.md)。不再使用旧 app 配置、Makefile 或未交付的 demo.sh。

六类状态应经过真实接入、投影和查询链路；四类 inspect 应查询到真实终态及持久结果，同幂等键同参数返回同一任务。取消意图与最终取消分开检查。断网补传保留原 bbolt 队列、epoch 与实体版本，不能通过重建空 DB 伪造恢复成功。

原 A01—A28 结果见 [业务证据](../../verification/2026-09-10/acceptance.md)，后续修复见 [全面复核](../review/2026-09-12/acceptance.md)。搜索、网页和混合场景单独验收，入口见 [工程验证边界](../production-readiness-checklist.md)。报告只证明对应日期、提交与范围，学员在自己的环境重新执行并保存结果。

## 4. 性能报告最小内容

记录CPU型号/核心、内存、OS、Go与依赖版本；消息编码大小（含分布）、实体数、来源数、每源速率、batch、topic分区、各进程数、订阅者数及过滤集合。稳态/补报/扇出分开运行，不混算不同场景P99。

| 测量 | 起点→终点 |
| --- | --- |
| 受理延迟 | Ingest收到批次→其Kafka确认对应ACK |
| 可见延迟 | 模拟事件产生→独立查询/订阅端观察到同实体相应或更高版本 |
| 下发延迟 | task创建事务提交→执行方inbox持久ACK |
| 恢复时间 | 恢复连接/启动进程→队列/Lag回到约定基线 |

合并中间帧时，只统计实际可关联的观测样本，报告覆盖率/被合并数；不能给未观测版本编造延迟。至少保留一份100events/s基线和一档加压结果；原5k/50k、P99目标可未达，但须报告瓶颈和错误行为，不能改输入定义让数字好看。

## 5. 完成判定

对应核心实现完成且A01—A28有证据，才称“校招核心完整版本”。某机缺CGO、Docker或依赖时，对应项保持待验证，并记录可在Linux CI执行的命令；不能把缺环境算成业务通过。完成后新增实体类型、任务类型或多实例部署都需要重新列验收项，不自动继承这份通过结论。
