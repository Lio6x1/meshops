# Kafka 消费优化对照与最终取舍

2026-09-13；原实现基线 `0f9589a5a80e98f40ba6ee4a3a0450511918c1fb`。

**最终保留有界批量提交位点，历史按实体并行候选暂缓合入。** 批量提交改善了完成吞吐和状态可见延迟，但本机 2,000 events/s 两轮仅一轮通过，因此不宣称稳定达到该容量，也不宣称百万实体高频上报已通过。

## 相同条件下的六轮结果

每轮 10,000 个混合实体、10 个来源、3 个 Kafka 分区、一个 Entity 进程。先完整预热并验证 10,000 个实体快照，再以 2,000 events/s 生成 30 秒，总计划 60,000 条。沿用原验收：无丢弃、无错误、无观测缺失，最终两组 lag=0，生成及完成吞吐至少为目标的 95%。没有放宽阈值。

全部 Entity、Ingest 与压测器共用 runner **4 CPU / 3 GiB**；MySQL 2 CPU / 1 GiB，Kafka 2 CPU / 1.5 GiB，Redis 2 CPU / 1.5 GiB（maxmemory 1 GiB）。每轮全新专用数据卷；保留原持久化配置。原有网页演示始终运行，性能轮次期间没有并行运行构建或真实依赖集成测试。各轮使用同一个标准验证器，没有前一份消费者实例对照中的临时耗时插桩；不能直接比较两份报告的绝对值。

| 轮次 | 版本 | 接收数 | 生成队列丢弃 | 完成吞吐（条/秒） | 可见延迟 P99 | 验收 |
| --- | --- | ---: | ---: | ---: | ---: | --- |
| 1 | 原实现 | 60,000 | 0 | 1,570.4 | 3,607.9 ms | 吞吐不足 |
| 2 | 仅批量提交 | 60,000 | 0 | 1,989.8 | 424.4 ms | 通过 |
| 3 | 批量提交＋历史并行 | 58,416 | 1,584 | 1,937.9 | 1,097.0 ms | 生成队列丢弃 |
| 4 | 批量提交＋历史并行 | 58,965 | 1,035 | 1,957.5 | 968.8 ms | 生成队列丢弃 |
| 5 | 仅批量提交 | 59,742 | 258 | 1,983.2 | 728.0 ms | 生成队列丢弃 |
| 6 | 原实现 | 60,000 | 0 | 1,695.6 | 3,644.4 ms | 吞吐不足 |

六轮 publish errors、observation misses 和最终投影/历史 lag 均为 0，完整预热快照均为 10,000。丢弃发生在压测生成队列，表示未完成计划负载，不能描述成 Kafka 已接收后丢失。原实现均值约 1,633.0，批量提交均值约 1,986.5，描述性均值差约 +21.6%；存在丢弃和小样本波动，不能包装成可靠容量提高 21.6% 的保证。

完成吞吐按 accepted / elapsedIncludingDrainSeconds 计算，包含生成、工作队列处理、观测等待和排空。可见延迟为抽样的“计划生成到可查询状态”，不是单纯 Redis 或 Kafka 的服务耗时。只测了 2,000 档，没有测最大吞吐、5,000 档、百万实体或多机容量。历史仍只对 100 个实体采样，不等于每条上报都写 MySQL。

`analysis.json` 中的 sampledHistoryCounterMax 是每 5 秒采样得到的累计计数最大值，可能缺少最后一个采样区间，不能当作最终历史行数。没有从这些不完整计数推导精确写入放大比例。

### 最终版本的较低负载补测

同样 10,000 实体、资源和 30 秒时长，仅批量提交版本补跑 1,500 events/s，每轮计划 45,000 条。

| 轮次 | 接收数 / 丢弃数 | 完成吞吐 | 可见 P99 | 整轮结论 |
| --- | --- | ---: | ---: | --- |
| 7 | 45,000 / 0 | 1,488.8/s | 97.7 ms | 业务负载通过，但 resourceSampleErrors=1，证据采样不完整，整轮失败 |
| 8 | 45,000 / 0 | 1,494.0/s | 71.9 ms | 完整通过 |

两轮发布错误、观测缺失和最终 lag 都为 0。这提供一轮 1,500/s 完整通过记录，不构成长期稳定性、生产 SLA 或容量上限保证。第 7 轮失败未删除，也没有因为业务数据正常就覆盖驱动脚本的失败结论。补测命令由 `reproduce/run-floor.py` 记录。

## 保留与回退

最终 `internal/bus/commit_batch.go` 每分区累计最多 100 条成功记录，或在健康消费循环等待 100ms 后同步提交下一位点。空闲时也会刷新；应用失败先提交此前成功前缀；提交故障重试同一批次，不继续推进。取消/重平衡时未提交记录会重放，业务幂等仍是必要条件。

历史并行候选实现了按实体锁、短全局锁、预算预留/退款，并通过真实 MySQL 锁隔离、同实体串行、失败退款和 race 测试。但两轮负载都出现生成队列丢弃，比仅批量提交的观测表现差。不能仅凭这些样本确定根因一定是历史并行；共享 CPU、数据库写入节奏和生成器排队仍是干扰因素。考虑缺乏性能收益证据，最终恢复原历史代码，保留候选补丁供进一步调查。

规模 Compose 同时将 MySQL 健康检查从 localhost socket 改为 127.0.0.1 TCP，避免初始化临时实例提前健康。所有对照镜像都采用同样 TCP 检查。

## 正确性验证与范围

- 新增位点批次测试覆盖空闲刷新时限、成功前缀、批次条数上限、取消重放、保留缺口、代际失效、永久错误及同位点重试。
- 真实 Kafka 测试确认提交前取消后，同一消费组会重新读取未提交前缀，最终排空；分区隔离与快照起点回归也执行。
- 试验候选阶段执行了全仓 Windows 真实依赖回归（`tests/integration-final.jsonl`），全部测试通过，只有三个崩溃子进程辅助入口跳过；后续两个历史测试补验见 `tests/history-supplement.jsonl`。其历史实现当时是并行候选，不能把这份全仓日志当作最终回退版的逐字节验收。
- `tests/bus-race.txt` 是真实 Kafka 的 Linux race 验证；`tests/state-race.txt` 是历史并行候选的真实 MySQL/Redis race 验证。最终恢复的历史代码与基线相同，最终发布另补普通测试和课程指纹检查，结果由追加的 release-checks 记录说明。
- 全仓 `go vet ./...` 通过。独立代码审查未发现明确正确性回归，提出的提交时限、同实体串行与 Sample 退款覆盖缺口已补验。历史相关候选测试随补丁归档，不混入最终业务测试。

最终批量提交版本的 [release-checks.json](release-checks.json) 汇总了新增发布验收：Windows 普通测试为 176 个顶层测试通过，Linux 普通 race 为 178 个顶层测试通过；两者均有 23 个依赖/辅助入口跳过、0 失败；真实 Kafka race 10 个顶层测试通过，真实历史/MySQL/Redis race 4 个顶层测试通过。对应日志为 `tests/unit-final.jsonl`、`tests/unit-race-final.jsonl`、`tests/bus-race-final.txt`、`tests/state-race-final.txt`。1793 条课程文件指纹与工作树/Git 暂存区一致，链接检查无失效链接。此次未重跑最终版本的全包真实依赖 race，不能把候选日志或普通 race 的 SKIP 包装为此项通过。

早期失败保留在 `tests/integration.jsonl`：自定义 Compose 项目未传给保留缺口演练，导致它找不到 Kafka。修正 COMPOSE_PROJECT_NAME 后完整回归通过。Linux 夹具曾因两次 time.Now() 的纳秒差异被业务校验拒绝，修正为从同一个发生时间计算过期时间后重跑；该次原始短日志未单独保留，问题与解决方式记在 [排错文档](../../docs/troubleshooting/10-consumer-throughput.md)。`tests/history-baseline-red.txt` 确认同一锁隔离测试会在原有全局锁实现上失败。

## 文件与复现

每轮目录包含脱敏 summary、benchmark、资源采样和诊断 JSONL。`manifest.json` 记录原始/归档 SHA-256、镜像摘要及最终关键源码指纹。没有归档临时凭证、生成环境文件、测试数据库或二进制。`reproduce/history-parallel.patch` 是未合入候选的源码及测试差异，不是最终教程必抄文件。

在包含本次最终代码的仓库根目录，以 PowerShell 操作（需 Go、Python、Docker、Git，且本地有基线提交）：

```powershell
New-Item -ItemType Directory -Force .cache/consumer-opt | Out-Null
Copy-Item verification/2026-09-13-consumer-optimization/reproduce/* .cache/consumer-opt/
python .cache/consumer-opt/prepare.py
python .cache/consumer-opt/run.py
python .cache/consumer-opt/analyze.py
```

prepare 固定从原提交创建 baseline，batch 复制当前批量提交代码，both 再应用归档的历史候选补丁。使用全新的 `.cache/consumer-opt` 实验目录，不在上次已打过补丁的目录重复准备。脚本只管理 `meshops-consumer-opt` 项目；禁止同时运行两份相同项目的实验。驱动脚本退出正常不等于性能验收通过，必须检查每轮 outcome。原有网页演示未重启，运行中的服务仍是优化前镜像。
