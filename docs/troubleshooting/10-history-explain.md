# 10｜LIMIT 500 为什么还扫描 30,500 行

[返回导航](README.md) · 上一篇：[投影恢复](09-projection-recovery.md) · 下一篇：[Git 与 CI](11-git-ci.md)

## 现象和待验证的假设

历史维护按 sampled_at 或 occurred_at 任一过期删除。原 selector 是：

```sql
SELECT id FROM entity_history_samples
WHERE sampled_at < '2026-09-05 00:00:00'
   OR occurred_at < '2026-09-05 00:00:00'
ORDER BY id LIMIT 500 FOR UPDATE;
```

`LIMIT 500` 约束返回数量，不直接约束为找到这些行而扫描的数量。OR 涉及两个时间索引，排序又按 id；是否扫描很多行取决于统计信息、选择率及行排列，不能只看 SQL 文本就断言“没有索引”。

## 本轮实际执行计划

本机 MySQL 隔离库 `meshops_review_history_20260912` 中从课程表结构复制空表，生成 100,000 条合成数据，执行 ANALYZE TABLE 后测量。阈值固定为 2026-09-05。完整 SQL 与原输出见 [evidence](evidence/README.md)。

| 场景 | 原 OR 查询 | 独立时间范围查询 |
| --- | --- | --- |
| A：全部近期，无过期行 | index_merge/sort_union，返回 0 行，约 0.13ms | sampled 范围约 0.00992ms；occurred 范围约 0.00645ms |
| B：仅高 ID 的 10 行 occurred_at 过期 | index_merge/sort_union，返回 10 行，约 0.093ms | occurred 范围返回 10 行，约 0.0125ms |
| C：30,000 行 occurred_at 过期，另 30,000 行 sampled_at 过期 | PRIMARY 顺序扫描 **30,500 行**才返回 500 行；Limit 节点约 **10.1ms** | 两个 covering range 各扫描 **500 行**；Limit 节点分别约 **0.175ms / 0.174ms** |

C 在 B 后执行，仍保留 B 的高 ID 10 行过期数据。C 中 occurred 过期段是 ID 30001—60000，sampled 过期段是 60001—90000，所以按 PRIMARY 从头走，要先越过前面的近期行。原查询并非在所有数据分布下都做大范围主键扫描。

这是一次本地 SELECT 的执行计划证据，时间受硬件、缓存、统计信息和并发影响。测量用的 `EXPLAIN ANALYZE` **去掉了 FOR UPDATE**，不能据此声称实际锁了 30,500 行，也不能把比例当作生产事务的固定加速比。原锁定 selector 另有 `EXPLAIN FORMAT=JSON` 计划，但它不提供实际锁等待测量。

## 最小修复

沿已有 sampled_at、occurred_at 索引分别选取有序范围页，在同一删除事务中保持**合计最多 500 行**的预算。保留“任一时钟过期即应删除”的语义；匹配两种过期条件的行不能重复消耗删除事实。对应样本的去重 key 和 sample 仍原子删除，任一分支积压都要触发后续维护。

这里没有新增一个猜测有用的重复索引。InnoDB 的 sampled_at 二级索引携带主键信息，本轮计划已显示 covering range。更改索引前应先确认现有迁移、EXPLAIN 和真实数据分布。

## 安全复现方式

先阅读 [归档说明](evidence/README.md)。归档 SQL 含建测试库、建表、INSERT/UPDATE/ANALYZE，**不是只读诊断脚本，也不支持原样重复运行**。只在明确属于自己的隔离实例/库、已确认固定测试库不存在时执行；不对课程事实表或生产表运行夹具。连接参数通过本机受控配置提供，不把密码写进命令记录。

需要先看现有表的估算计划时，下面只生成计划，不执行锁定读取或修改数据；先选择自己的测试库：

```sql
EXPLAIN FORMAT=JSON
SELECT id FROM entity_history_samples
WHERE sampled_at < '2026-09-05 00:00:00'
   OR occurred_at < '2026-09-05 00:00:00'
ORDER BY id LIMIT 500 FOR UPDATE;
```

`EXPLAIN ANALYZE SELECT ...` 会实际执行读取，应只对已知规模的隔离数据使用本页归档中不带 FOR UPDATE 的版本。不要把它推广成可安全执行任意 UPDATE/DELETE 或锁定查询的前缀。

```powershell
go test ./internal/state -run 'TestHistoryRetentionBothClocksAndCutoff|TestMySQLSampleIdempotencePagingAgeAndBudget' -count=1 -v
```

实际本轮包含双时钟、cutoff 与积压回归的定向组通过（state 15.791s），需要真实 MySQL 配置。回归检查精确边界、仅一个时钟过期、重复匹配以及无孤立去重 key；微基准不是这些正确性断言的替代品。

排错记录保留行数分布、cutoff、索引/分区、估计与实际行数、排序方式、事务耗时和等待类别；不要复制包含真实业务字段的整段 SQL 诊断。实现：[history.go](../../internal/state/history.go)。
