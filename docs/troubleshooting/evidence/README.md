# 历史清理 SQL / EXPLAIN 归档

[返回问题讲解](../10-history-explain.md) · [知识库导航](../README.md)

这四个文件复制自 2026-09-12 本轮审查的 `.cache/review/` 同名文件，写入工作树时已用 SHA-256 确认副本逐字节一致。仓库统一的文本 LF 规则可能在入库时规范换行，不改动 SQL、计划数值或输出中的字面 `\n`。已检查内容：只有合成夹具、SQL、执行计划和固定测试库名，没有连接密码、token 或真实业务 payload。其他原始运行日志未复制进仓库。

| 文件 | 内容与顺序 |
| --- | --- |
| [history-explain.sql](history-explain.sql) | 创建隔离测试库/表，生成 100,000 行，依次测 A 无过期、B 高 ID 稀疏过期；末尾保留原 FOR UPDATE 查询的 JSON 估算计划 |
| [history-explain.txt](history-explain.txt) | 上述 A/B 的原始输出 |
| [history-explain-dense.sql](history-explain-dense.sql) | **接着 A/B 后执行**，将两个各 30,000 行区段改为不同时间列过期，测 C 密集积压 |
| [history-explain-dense.txt](history-explain-dense.txt) | C 的原始输出，PRIMARY 扫描 30,500 行与两个范围各 500 行 |

## 使用边界

SQL 夹具含 CREATE DATABASE/TABLE、INSERT、UPDATE、ANALYZE TABLE，不能当作只读诊断直接投向现有业务库。库名固定为 `meshops_review_history_20260912`，表结构来自 `meshops_course.entity_history_samples`；只有确认隔离实例和该固定库归属、不会接触他人数据后才考虑执行。首份脚本不是幂等的，`CREATE TABLE` 第二次会失败；不要为方便重跑添加自动 DROP 或清理真实数据。

归档保留原始脚本与输出，没有把“安全提醒”插进历史 SQL。准备新的独立夹具时可复制到自己临时文件并明确更换库名，但那是新实验，不能再标为此处原始证据。连接使用本机受控配置；归档不提供明文连接命令。

`EXPLAIN ANALYZE` 的各 SELECT 都不带 FOR UPDATE，但会实际读取数据；最后的 `EXPLAIN FORMAT=JSON ... FOR UPDATE` 只输出计划。这里没有真实事务锁数量或并发写延迟数据。输出里的字面 `\n` 来自原 mysql 批处理格式，刻意不重排为手工美化版本。

C 在 B 后运行，所以除了两个大区段外，B 的最后 10 行旧 occurred_at 仍保留。不同机器、统计信息、缓存和数据顺序可能得到不同计划与时间，应报告自己的结果，而不是把 10.1ms 或 0.175ms 当成验收阈值。
