# 账号与已有回归验收证据（2026-09-13）

此目录归档个人账号、真实 HTTP 功能、场景控制、Go 测试、教程复制和容量实测。最新普通测试为 [168 PASS / 22 SKIP / 0 FAIL](unit-release.summary.json)，跳过不计作通过。独立容量共 [8 次记录](capacity/README.md)：一万实体 2000/s 与十万实体 500/s 对应档位通过；百万初始化及持久接收完成，但消费积压排空失败，不能称为百万端到端容量通过。完整真实依赖 race 仍等待发布提交的 CI 验收。

## 测试结果与边界

| 运行 | 顶层测试 pass / skip / fail | 全部测试节点 pass / skip / fail | 仅子测试 pass / skip / fail | 包级 pass / skip / fail |
| --- | ---: | ---: | ---: | ---: |
| Windows 真实依赖集成 | 216 / 3 / 0 | 341 / 3 / 0 | 125 / 0 / 0 | 16 / 17 / 0 |
| Linux 普通 race | 162 / 22 / 0 | 260 / 22 / 0 | 98 / 0 / 0 | 16 / 17 / 0 |

顶层测试指 `Test` 不含 `/` 的测试；全部测试节点包含顶层测试及子测试，不能把两列相加。包级 17 个 `skip` 是没有测试文件的包，不计入测试用例跳过。

- [Windows 结构化摘要](integration-windows-final.summary.json)及[全部事件元数据](integration-windows-final.events.jsonl)：时间范围为原始记录的 `2026-09-13T13:10:12.3585458+08:00` 至 `2026-09-13T13:21:29.6759628+08:00`。真实依赖验收门禁的 **23 个 REQUIRED 测试全部 pass**，没有业务测试 skip；3 个 skip 均为 edge/state/tasks 的 `TestDurableCrashChild` 子进程辅助入口。摘要保存每个 REQUIRED 测试的包名、测试名、Elapsed、Time，以及检查脚本当时的 SHA-256。
- [Linux 结构化摘要](linux-race.summary.json)及[全部事件元数据](linux-race.events.jsonl)：时间范围为 `2026-09-13T05:29:36.961429191Z` 至 `2026-09-13T05:30:22.206369581Z`。执行范围为普通 `go test -race ./... -json -count=1 -timeout=10m`，**没有 integration build tag**。按原始事件重新核对，22 个测试 skip 中，2 个是 edge/state 的崩溃辅助入口，20 个需要外部依赖；tasks 的辅助入口只出现在 integration 标签运行中。仅 integration 标签下存在的测试也不属于此运行；因此这份结果不能称为“真实依赖 race 全通过”。

可直接用脱敏事件重新验证 Windows 的 REQUIRED 门禁：

```powershell
python -B scripts/check-test-results.py verification/2026-09-13-accounts-scale/integration-windows-final.events.jsonl
```

## HTTP 与场景结果

以下 JSON 已逐字段检查，未发现实际密码、Cookie、CSRF、内部令牌或访问凭证；本次均为原始字节一致的复制。`password_csrf`、`old_cookie` 等阶段名称属于公开测试标签，不是秘密值。

| 文件 | 已记录结果 |
| --- | --- |
| [accounts-http-final.json](accounts-http-final.json) | 30 个账号阶段通过，清理通过，耗时 3.401 秒；包含随机操作员停用、旧会话撤销、首次改密及权限边界 |
| [fullstack-final.json](fullstack-final.json) | 10 个检查通过，耗时 12.214 秒；六类快照、四类 inspect 完成、执行中取消、历史、调度、CDC 收敛和角色边界 |
| [test-motion-final.json](test-motion-final.json) | 30 个实体快照；四种移动类型的位移、sensor/facility 的固定位置，来源状态已恢复 |
| [test-scene-counts-final.json](test-scene-counts-final.json) | 30 个混合实体、20 个可执行与 10 个仅观测绑定、四种新增绑定任务、数量上界/缩减/归零及保留事实 |
| [test-simulation-final.json](test-simulation-final.json) | 六项模拟控制检查通过；暂停、离线积压、快照过期及恢复排空，恢复至 running、数量 1 |

这些文件保留测试任务 UUID、演示实体 ID、版本、时间戳和位移等非秘密事实，以便追踪测试链路；不将这些功能检查当作压测。

## 保留的早期失败

[early-failures.json](early-failures.json)保留两次真实失败运行的结果、失败测试节点、计数、原始 SHA-256，以及固定诊断标记在原始 JSONL 中的行号。它们没有被改写为通过，也没有归为业务逻辑缺陷。

| 原始运行 | 顶层 pass / skip / fail | 已观察原因 |
| --- | ---: | --- |
| `.cache/account-scale/integration-windows.jsonl` | 211 / 3 / 2 | Docker 命名管道访问被拒绝；原始记录出现 `Access is denied`、`permission denied`、`docker_engine`。受影响的是保留截断相关测试，含一个失败子测试 |
| `.cache/account-scale/linux-race-missing-fixture.jsonl` | 161 / 22 / 1 | race 测试镜像缺少 `compose.demo.yml` 夹具，原始记录出现该文件名和 `no such file or directory` |

后续最终运行已通过各自范围的测试。这里保留的是可观察的执行环境/夹具失败，不据此声称曾存在或修复了对应业务 bug。

## 注册初始化与解析测量

[initialization/summary.json](initialization/summary.json)将已有测量按源文件、行号、耗时与 SQL 次数整理，支撑[初始化复盘](../../docs/postmortems/2026-09-13-registry-initialization.md)中有源记录的数值。所有测量仅反映所测规模的初始化/解析成本，不外推百万实体或持续上报吞吐。

| 实体数 | 操作 | 修改前秒数 / SQL 数量 | 最终修改后秒数 / SQL 数量 |
| --- | --- | ---: | ---: |
| 1,000 | Seed | 7.8605848 / 4,004 | 0.4795905 / 12 |
| 1,000 | CheckBindings | 3.7290554 / 2,002 | 0.0395606 / 6 |
| 10,000 | Seed | 65.1939145 / 40,004 | 1.926309 / 84 |
| 10,000 | CheckBindings | 31.1899673 / 20,002 | 0.4651184 / 42 |

- [binding-baseline.txt](initialization/binding-baseline.txt)保留修改前 SQL 次数超预算的失败；这是性能预算回归测试的 RED 结果，与上面的两次环境失败分别记录。
- [binding-after.txt](initialization/binding-after.txt)保留中间通过运行；[binding-final.txt](initialization/binding-final.txt)是表格“最终修改后”数值来源；[binding-results.txt](initialization/binding-results.txt)说明测量边界。SQL 数量为独占服务端会话的 `Com_select + Com_insert + Com_update` 增量，排除迁移/准备、事务控制、SHOW STATUS 和驱动协议 prepare/close；耗时包含 Seed/CheckBindings 的提交，不包含迁移与注册表构造。
- [manifest-after.txt](initialization/manifest-after.txt)记录十万实体生成及加载 `388966000 ns/op`、`192363432 B/op`，单次 benchmark。`B/op` 是累计分配量，不是峰值 RSS。
- [manifest-before-top.txt](initialization/manifest-before-top.txt)记录修改前 CPU 样本：`yaml.v3.(*decoder).mapping` 累计 2.59 秒，占总样本的 80.94%。其 Build ID 中的本机用户名/临时构建绝对路径已删除。
- 本次提供的源文件中没有修改前 `BenchmarkScaleManifest` 的原始输出，因而未独立重建复盘中约 **2.739 秒 / 204 MB** 的数值。此缺口已写入初始化摘要；CPU top 和修改后的 benchmark 均有直接源记录。

`.cache/profiles/before.cpu` 为 9,758 字节，但其 profile 字符串表包含本机用户名和临时构建路径。因此只记录 SHA-256 与排除原因，不复制该二进制；`verification.test.exe` 也不归档。

## 溯源与脱敏规则

[manifest.json](manifest.json)列出每个原始相对路径、原始字节长度、SHA-256、归档目标、处理策略及归档文件 SHA-256。原始路径以本工作树/仓库根目录为基准；原始缓存不要求进入 Git，也未在本次操作中删除。

1. Go JSONL 保留原始事件顺序及每行已有的 `Action`、`Test`、`Package`、`Elapsed`、`Time`；完整删除 `Output` 和其他字段。`Action=output` 的行仍存在，但没有输出正文。随机测试数据、驱动诊断、命令输出不随之入库。
2. 五份 HTTP/场景 JSON 经过字段和值检查：密码、Cookie、CSRF、token、Authorization、access code、secret、credential 及独立 `pass` 密钥字段禁止复制；密码/令牌赋值和 Bearer 值也禁止复制。`passed` 布尔值和公开测试阶段名称保留。本次这五份 JSON 的剔除字段数为 **0**；每份已审查的字段路径均列在 manifest 中。
3. 初始化文本经过单独审查；只含测试名称、度量、时间和已说明的边界。CPU top 的 Build ID 是唯一修改过的文本字段，原始与归档 SHA-256 均保留。
4. 不读取或复制 `.cache/account-scale/run*.py`，不归档环境文件、密码脚本、完整 Go 原始日志、测试可执行文件，也不触碰正在运行的 scale 输出。

SHA-256 用于校验所持原始文件与派生证据是否对应，不是对运行环境或容量结论的数字签名。

## 后续普通测试与 P2 审核关闭

[unit-final.summary.json](unit-final.summary.json)和[脱敏事件](unit-final.events.jsonl)追加保存 `2026-09-13T14:11:57.243848+08:00` 至 `2026-09-13T14:12:46.8533958+08:00` 的普通 Go 全包运行：顶层 **165 pass / 22 skip / 0 fail**，全部测试节点 **271 pass / 22 skip / 0 fail**，其中仅子测试 106 pass；包级 16 pass、17 个无测试文件的 skip。22 个测试跳过已逐条核对为 **20 个外部依赖检查 + 2 个崩溃辅助入口**。摘要单列 RawGenerator、来源夹具缓存和 HTTP 鉴权截止时间的新测试结果。此前 Windows 216 顶层通过及 Linux race 162 顶层通过记录保留各自日期和范围，未被本轮普通测试替换。

[review-p2-closed.json](review-p2-closed.json)记录 P2 关闭：`CheckSession` 使用独立的 3 秒子 context，检查后取消；后续请求仍使用原 context，SSE 长连接不会被该短期限截断。阻塞 mock 对普通请求和 SSE 入口两路均验证超时返回 503，且没有转发业务请求。本轮归档直接记录两路各 3 秒通过、父测试 6 秒通过，`internal/web` 包为 8.721 秒。主任务另报告两路 RED→GREEN 的独立定向运行耗时 9.385 秒及最终审核 agent 只读确认；两种来源在审核记录中分开标注，未把报告说明冒充未提供的原始日志。

本次追加不是新的真实依赖 race 或 CI 验收，也不证明百万实体容量。Linux 派生摘要的 skip 分类已按原始事件重算；原始文件 SHA-256、旧事件文件、原有通过/跳过总数和时间范围均不变。
