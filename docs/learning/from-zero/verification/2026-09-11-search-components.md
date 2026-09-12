# 搜索组件进度与真实依赖测试

这是组件阶段记录，**完整搜索闭环与 Z10 课程尚未交付**。根工程新增内容未写入固定的 Z01—Z09 答案。

## 已有实现

- `internal/search/cdc.go`：Canal flat JSON 白名单投影，多行先全部校验再处理，非法库表/DDL/DELETE 拒绝；采用 UTC 时间与正数外部版本。
- `index.go`：本机 ES HTTP 客户端、有界响应、严格字段映射、重复/旧版本处理、同版本内容冲突报错。
- `handler.go`：任何行失败时整体返回失败，配合消费成功后提交位点的约定。
- `snapshot.go`：短读锁内确定 binlog 坐标与一致性读视图，释放锁后再保存起点和写 ES。UNLOCK 失败会丢弃连接，避免带锁连接回到池中。
- `query.go`：租户强制过滤、关键词/状态/实体/创建时间筛选、PIT + search_after、有界页大小和固定到期的签名游标。
- `service.go` 与 `proto/search/v1/search.proto`：SearchTasks RPC 处理，可信身份取租户，未就绪拒绝查询；生成的 Go 协议文件已提交到工作目录。
- `internal/bus`：增加严格消费模式，缺少历史时停止；原消费模式仍保留原行为。
- `compose.search.yml`：可选 ES/Canal 配置草案，ES 已实际启动测试；Canal 参数还需首次导入脚本与真实链路验证。

## 已验证

证据在根目录 `.cache/search-foundations-20260911/`：

| 证据 | 覆盖内容 |
| --- | --- |
| `unit-with-search.txt`、`vet-with-search.txt` | 加入搜索组件后的根工程全量单元测试、go vet 通过；随后严格消费新组边界另做专项测试 |
| `search-integration-final.jsonl` | 搜索包 12 项顶层测试 PASS，无 SKIP；其中实际使用 ES 与 MySQL 的两项见下文 |
| `es-query-integration.jsonl` | 真实 ES 8.19.4：映射、重复/乱序/版本冲突、中文关键词、租户隔离、同时间分页与分页中新增任务 |
| `snapshot-after.jsonl` | 真实 MySQL：释放锁后并发修改任务，快照仍读取修改前内容，坐标存在 |
| `strict-after.jsonl` | 真实 Kafka 临时 topic：旧消费组、新严格消费组遇到被截断的历史均返回 RetentionGap；正常模式仍可处理保留后缀 |
| `service-after.jsonl` | RPC 处理层的角色、可信租户与就绪检查；尚不是已启动 Search 进程的跨网络测试 |

ES 测试创建独立临时索引并清理，MySQL 测试创建独立临时数据库并清理，Kafka 使用独立临时 topic。没有清空项目数据。Canal 1.1.8 镜像已成功拉取；此前 Docker manifest 检查的“找不到”结果不能当作镜像不可用结论。

## 必须继续完成

1. 首次导入的持久化起点/完成标记、独立 Canal 复制账户、单分区 CDC topic 初始化与启动脚本。
2. 启动实际 Canal，核实 flat JSON 和时间格式，验证 MySQL → Canal → Kafka → ES；确认消息失败不提交位点及恢复行为。
3. Search 进程装配、健康检查/指标、opctl 搜索命令、跨网络权限验证与故障恢复测试。
4. Z10 完整文件答案、详细讲义、从 Z09 升级步骤和连续复制验证。
5. 统一复核项目和教程；不把本报告的组件测试视为完整闭环验收。

部署候选与正确性约定见 [任务搜索契约](../../../superpowers/plans/2026-09-11-task-search.md)。
