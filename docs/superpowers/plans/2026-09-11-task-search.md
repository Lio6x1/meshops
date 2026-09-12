# 任务搜索实现契约

> 按日期保留的设计/实施记录，不作为当前待办或启动指南。当前工程已演进至 Z13；旧路径和阶段状态按当时上下文理解，后续状态见 [记录说明](../README.md)。

状态：基本链路、依赖停机恢复、查询、维护重建、Z10 教学交付、独立学习环境升级复验及本次统一复核已完成，边界见 2026-09-12-integrated-review.md。用户已授权一个服务、一个任务索引及对应课程。

## 功能与边界

Search 只查询任务实例：`payload.note`、`cancelled_reason`、`failure_reason` 全文检索，结合任务状态、目标实体、创建时间过滤。TaskDefinition.description 是类型目录说明，不加入实例搜索；固定 inspect 的结果仍保持已有结构。

MySQL 是事实来源，Search 是最终一致的只读投影。原 Create/Cancel/Report 事务及 Outbox 不依赖 ES，搜索不可用返回明确错误，不悄悄改为 MySQL LIKE。详情和操作仍回到 Task 服务验证。

沿用 gRPC、现有凭证/租户上下文和本机访问限制。SearchTasks 请求不能指定租户，服务端从 operator/admin 身份取租户。不接受原始 ES DSL。关键词最长 256 UTF-8 字节，页大小 1—100；创建时间采用左闭右开区间。

## 同步

MySQL ROW/FULL binlog → Canal 1.1.8 flat JSON → 独立单分区 Kafka topic → Search 内索引消费者 → ES 8.19.4。此组合已通过本机真实链路验证，版本选择以兼容和可复现为依据。

只订阅指定数据库的 tasks 表。Canal 使用独立复制账户；binlog 时区固定 UTC。flat JSON 的一条消息可能有多行，逐行验证；整条消息写入成功才提交 Kafka offset。重试不能提前推进 offset。错误行和任务库 DDL 阻断该分区并报错；Canal 转发的其他数据库 DDL（无数据行）可跳过。当前业务没有物理删除任务能力，因此 DELETE/TRUNCATE 不支持，必须停止并要求修复或重建；不实现会被旧消息复活的临时 ES 删除。

索引文档只保存白名单字段，不把原始 payload/result/凭证直接投入索引。文档 ID 编码 tenant_id 与 task_id，避免分隔符碰撞。使用 status_version+1 作为 ES external version（MySQL 初始版本可为 0）。新版本覆盖，旧版本忽略；相同版本必须比较规范化投影内容，内容冲突时报错，不能把所有 409 都当幂等成功。

## 首次导入与恢复

全量导入前，在短暂写锁内获取 binlog 起点并开启一致性快照，再释放锁。Canal 从该明确起点读取，位点文件持久化。全量快照和增量使用相同版本协议，后到的旧快照不能覆盖新状态。全量成功前不开放搜索就绪状态。禁止先无保护地导出全量、再从当前最新 binlog 启动。

重启复用 Canal 位点和 Kafka 消费组；ES 成功而 offset 未提交时允许重复。记录已处理数、重复/过期版本数、失败数、消费进度和就绪状态。binlog/Kafka 已清理导致恢复起点缺失时明确失败并重建，不宣称 lag=0 就代表完整。初版重建在维护窗口停搜索、保留任务写链路，仍只使用一个任务索引，不扩展双索引在线切换系统。

维护流程已落地为 `scripts/rebuild-search.ps1`：先停止 Search/Canal 并使标记失效，清理专用 Canal 元数据，记录 CDC 末尾，再重建快照。确认 CDC 末尾未变化、消费组无成员，才重置该组起点并发布完成标记。保留任务事实和原有业务 topic；不要求删除 CDC topic。快照记录的 `cdc_start` 也是消费组位点过期后的最早重放边界。

## 查询

使用 PIT + search_after，排序创建时间和任务 ID。游标签名并绑定租户、完整筛选条件、页大小、PIT 和到期时间；有效期有界，不能每次翻页无限延长。查询强制 tenant filter；过期 PIT 返回明确重新查询提示，末页关闭 PIT。

## 验收与教学

- 解析：批量行、非法字段、错误库表、DDL/DELETE 拒绝、UTC 时间、版本上界、不可泄漏字段。
- 索引：重复/乱序、同版本冲突、ES 暂停恢复、写成功提交前崩溃、首次全量期间并发更新。
- 查询：跨租户、签名篡改、筛选变化、同时间分页、PIT 过期、ES 不可用。
- 真实链路：创建备注任务 → 搜索可见 → 状态更新 → 搜索更新；重启 Canal/Search 后补齐。
- 冻结 Z09 后追加 Z10 的详细讲义、完整代码、增量迁移、启动/排错和复制测试，最后统一复核。

## 官方依据

- [Canal 1.1.8 发布说明](https://github.com/alibaba/canal/releases/tag/canal-1.1.8)
- [Canal Kafka 配置](https://github.com/alibaba/canal/wiki/Canal-Kafka-RocketMQ-QuickStart)
- [ES external version](https://www.elastic.co/guide/en/elasticsearch/reference/8.19/docs-index_.html)
- [PIT 与 search_after](https://www.elastic.co/docs/reference/elasticsearch/rest-apis/paginate-search-results)
