# 六类实体数量配置验收

范围：每类 0—5 个、默认 1 个、共最多 30 个的混合模拟场景。真实数据沿原 bbolt → gRPC → Kafka → Entity/Redis → HTTP/SSE 链路流转，四类执行器各绑定五个实体。这里不代表一万个混合实体的性能结论。

## 真实运行结果

| 验证 | 结果与证据 |
| --- | --- |
| 三十实体与数量控制 | [9 项全部通过](scene-counts-http-final.json)：六类各五个新鲜快照；20 个执行绑定、10 个只读观测绑定；四类 `-005` 巡检成功；6 被拒绝；缩减后保留记录；全零清单；恢复原设置 |
| 原有前后端流程 | [10 项全部通过](scene-counts-fullstack.json)：双角色会话与权限、六类快照、四类任务成功、执行中取消、CDC 搜索收敛、退出会话 |
| 模拟器故障恢复 | [6 项全部通过](scene-counts-recovery.json)：暂停、断网缓存、过期、恢复补传；恢复原数量 1 与 running 模式 |
| 并发控制 | [Linux race 通过](scene-counts-race.txt)：simulation、edge、web、web-gateway；Store 测试连接真实 Redis，未跳过该测试 |
| 旧环境升级 | [隔离 MySQL 测试通过](scene-counts-integration.txt)：六→三十、重复执行、拒绝删除/归属变化、凭证冲突回滚、旧任务/Outbox/哈希保留 |
| 六类 RPC 与投影 | [真实 Kafka/Redis race 通过](scene-counts-six-rpc.txt)：全部 30 个实体经过认证、适配、Kafka 与 Redis 投影 |
| 单元与静态检查 | [全 Go 普通测试通过](scene-counts-unit-2.txt)；修改的手写包 staticcheck v0.7.0 退出 0。普通测试未配置外部依赖门控，不等同全仓真实依赖验收 |
| 从 Markdown 重建 | [13 阶段复制并构建通过](course-result.json)，Z12/Z13 前端各 18 项测试通过、类型检查与生产构建通过 |
| 前后端细分步骤 | [5 个 Web 小步骤全部通过](substeps-result.json)，逐步复制后执行指定 Go 测试与前端构建 |

Docker 实际旧数据环境已增量 Up 成功，最终版本再次 Up 也成功，原卷、凭证与搜索初始化标记保留。运行结束页面恢复六类各 1 个、正常上报、待补传 0。

## 浏览器检查

通过浏览器控件逐类设置 5 并提交，确认目标总数 30、实际总数 30。实体总览显示 30 个新鲜快照、20 个执行实体、六类各 5；地图有 30 个可点击点位，密集场景默认隐藏 ID 文字，悬停或选择可查看。全零后地图点位和清单清空，不沿用旧 SSE 状态；再恢复每类 1 个。390px 视口下页面宽度 375px，无整页横向溢出，六个输入框均为 min=0、max=5。已恢复原视口。

## 失败尝试与可追溯性

- 第一次 Up 因普通 seed 严格拒绝来源映射扩充而停止。新增显式受限增量注册后成功；见[问题记录](../../postmortems/2026-09-12-scene-counts.md)。
- 第一次 HTTP 验收误读 `task.status`，脚本修正后重新运行，最终报告单独保存。
- 初次合并集成命令中 MySQL 测试通过，但 Kafka 使用错误端口 9092，文件保留失败结果；按 Compose 内部端口 29092 重跑后的独立 RPC 报告全部通过。
- 首次课程复制在 npm 全局缓存目录遇 EPERM。设置 `npm_config_cache` 到项目可写缓存后，从新的空目录重新完成全程，未把失败当通过。

本机原始运行日志保存在 `.local/archives/2026-09-12-scene-counts/`；脱敏后的关键 JSON 和测试输出已随仓库保存于本目录，清理 `.cache` 不会丢失这些证据。访问码、Cookie、CSRF、数据库口令不在归档报告中。
