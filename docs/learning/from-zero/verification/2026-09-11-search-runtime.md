# 任务搜索真实链路记录

本报告在组件验证之后补充实际进程和容器验证。**Z10 教程、显式维护重建/不完整初始化恢复，以及全项目最终复核仍待完成。**

## 运行方式

先按根 README 完成基本初始化。如果旧进程在运行，先执行 `./scripts/stop.ps1`。从仓库根目录执行：

```powershell
./scripts/initialize-search.ps1
./scripts/build.ps1
./scripts/start.ps1 -Simulators -Search
./scripts/demo-search.ps1
./scripts/test-search-recovery.ps1
```

查询当前任务：

```powershell
. ./scripts/search-env.ps1
./bin/opctl.exe task search --keyword '屋顶' --entity drone-001 --page-size 10
./bin/opctl.exe task search --status SUCCEEDED --start '2026-09-11T00:00:00Z' --end '2026-09-12T00:00:00Z'
```

默认启动仍为原四个服务；`-Search` 增加第五个 Search 服务，RPC `127.0.0.1:50055`，健康/指标 `127.0.0.1:18084`。ES 仅发布回环地址 `127.0.0.1:19200`。任务详情和操作以 Task/MySQL 为准，搜索是最终一致投影。

初始化依次校验 ROW/FULL、配置独立复制账户、创建单分区 CDC topic、创建索引、导入快照并保存 binlog 起点。标记未完成时拒绝启动。Search 将实际索引 UUID 与标记绑定，同名空索引不能冒充原索引。Canal 位点与 TSDB 存在独立 Docker 卷中。

## 实际发现并修正的问题

1. Canal TSDB 启动会枚举系统库。按其实现使用系统库黑名单跳过，无需扩大业务读取权限。参见 [官方 DatabaseTableMeta 实现](https://github.com/alibaba/canal/blob/canal-1.1.8/parse/src/main/java/com/alibaba/otter/canal/parse/inbound/mysql/tsdb/DatabaseTableMeta.java)。
2. Canal 将数据库密码用于复制注册报文，MySQL 8.0 对此字段限制为 32 字符。生成 24 字节随机数据并 Base64 编码得到 32 字符，保留 192 位随机性；不是弱密码或 MD5 密码哈希。参见 [MySQL 字段定义](https://github.com/mysql/mysql-server/blob/8.0/sql/rpl_source.h)、[长度常量](https://github.com/mysql/mysql-server/blob/8.0/sql/sql_const.h)。
3. 真实 Canal flat JSON 把 ENUM 状态输出为数字字符串。现在同时核对 mysqlType 中完整枚举顺序再转换；新增 `TestCanalMySQLEnumOrdinal`，未知顺序不能静默解释。
4. 解析失败时原来只能看到 Kafka 重试位置。Search 现在额外记录已脱敏的失败原因，保留 topic/partition/offset 定位信息，不打印原始任务 payload。
5. Canal 的表过滤仍会转发其他数据库的建库/删库 DDL。隔离 MySQL 测试曾因此阻塞消费者；现只跳过明确属于其他库、没有数据行的 DDL，任务库 DDL 和错误库表的数据行继续拒绝。新增 `TestCanalIgnoresForeignDatabaseDDL`，避免把无关控制事件当作任务数据损坏。

## 验证结果

- `results/search-demo-7f85d25fa1814cd0b0c402374dae5371.json`：新建真实 inspect 任务，搜索经 Canal/Kafka 获得更新，任务最终成功，搜索状态与版本和 Task 一致。
- `results/search-demo-dde1a9d904ff48b99a58e348e23686f4.json`：处理其他数据库 DDL 后的最终重启复跑通过，积压控制事件与后续任务均继续消费。
- `results/search-recovery-601ba2a9f7e2466cbad58bf1becbf934.json`：实际停止 Canal、ES 各一次。停止期间 Task 都成功执行；Canal 恢复后补齐遗漏消息，ES 停止时查询明确 Unavailable，恢复后消费与索引追平。
- `.cache/search-foundations-20260911/search-runtime-integration.jsonl`：真实 ES/MySQL 的搜索组件验证，包括组合过滤、时间上界排除、分页中插入、UUID 替换拒绝和首次快照。
- 最新搜索包运行共 16 项顶层测试 PASS，无 SKIP；全根单元测试与 go vet 记录分别在 `unit-runtime-final.txt`、`vet-runtime-final.txt`，之后的 DDL 修正另做完整搜索包和真实依赖复跑。
- `.cache/search-foundations-20260911/search-metrics.txt`：最终构建实际导出 CDC 完成记录数、索引结果和查询耗时计数。验证结束后本轮业务进程已通过所有权检查停止，`.local/processes.json` 为 `[]`，数据卷保留。
- `.cache/search-foundations-20260911/cli-search-after.jsonl`：命令行调用实际本地 gRPC 测试服务器并输出真实响应。
- `.cache/search-foundations-20260911/enum-before.txt`、`enum-after.jsonl`：ENUM 数字格式的失败复现与修正回归。

早先两次 demo 因上述 Canal 配置/ENUM 适配问题超时，修正后才获得当前 PASS 记录，没有把超时当作成功。

## 限制与后续

- 还需提供不完整初始化、索引/位点丢失后的明确维护重建命令与测试。当前检测到缺口或索引身份变化会拒绝继续；这是检测能力，不能声称已经自动修复。
- 数据库是本地教学部署；ES/Docker 内通信未配置生产 TLS，机器凭证在忽略提交的 `.local` 文件中。
- 当前只有一个任务索引；没有新增全平台搜索、算法调度或真实设备能力。
- 搜索指标已在最终构建核对导出；教程发布与整体复核不以链路演示替代。
