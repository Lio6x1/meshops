# 全栈运行证据归档

本目录记录前端、HTTP 接入和 Compose 演示部署的新一批证据。**已归档 16 份完成来源，包括明确保留的本地 Linux 集成失败。** 结果与适用边界见 [本机验收报告](report.md)，脱敏明细见 [results.json](results.json)。旧后端审查与本次全栈运行是不同批次，不能沿用旧 PASS 代替新代码的验收；本次最终云端 CI 另待实际结果。

## 待归档来源

| 来源 ID | `.cache/fullstack/` 中明确允许的文件 | 发布的字段与范围 |
| --- | --- | --- |
| `linux-race` | `linux-race.jsonl` | 去除 Output 的逐测试终态、包结束完整性、失败与跳过数量；不把普通测试的集成 SKIP 当真实依赖测试通过。 |
| `http-first` | `http-e2e-first.json` | 十组固定 HTTP 检查的名称与通过状态；覆盖角色权限、六类实体、四类执行、取消、CDC 与会话撤销。不会公开原始 API 详情或会话数据。 |
| `http-after-rebuild` | `http-e2e-after-rebuild.json` | 搜索重建后独立执行的同样十组 HTTP 检查，不冒充重建前的运行。 |
| `http-after-restart` | `http-e2e-after-restart.json` | 整栈重启后独立执行的十组 HTTP 检查。 |
| `docker-build` | `docker-build-final.txt` | 历史镜像构建命令，非最终 release 构建。 |
| `docker-build-release` | `docker-build-release.txt` | 最后错误文字修正后的 release 镜像构建；原始字节哈希与明确提供的进程退出码。 |
| `docker-release-up` | `docker-release-up.txt` | 最终镜像实际 Up-NoBuild 启动、健康与宿主 HTTP 检查，命令退出 0。 |
| `final-release` | `final-release-proof.json` | 最终镜像启动后九项已有任务与搜索保留检查。 |
| `docker-first-up` | `docker-first-up.txt` | 原始字节哈希与明确提供的进程退出码；不从日志中的 Healthy 推断业务全流程成功。 |
| `docker-port-fix` | `docker-port-fix.txt` | 端口调整后命令执行的独立记录，不替代首次启动或 HTTP 流程。 |
| `linux-integration-race` | `linux-integration-race.jsonl` | 已退出 1 的历史容器集成竞态失败，保留终态与失败门禁，归为历史非必需来源，绝不改写为通过。 |
| `windows-integration` | `windows-integration.jsonl` | 宿主隔离项目的真实依赖集成验收及门禁；未结束时不读取为最终通过。 |
| `permissions` | `permissions-proof.json` | 实际 UID 的凭据文件可读/拒绝边界，以及重启后访问码保留检查。公开仅保留检查名称与布尔结果。 |

主任务已提供 `restart-proof.json`、`search-rebuild-proof.json`、`browser-proof.json`，分别登记为 `restart`、`search-rebuild`、`browser`。它们使用下述小型证明格式；归档仍需主任务明确确认生产结果已结束，不能仅以文件存在推断通过。

## 何时生成

[archive.py](archive.py) 默认只列出白名单文件是否存在，不读取结果正文、不复制、不写入：

```powershell
python docs/verification/2026-09-12-fullstack/archive.py
```

待每个生产结果的命令**实际结束并取得退出码**后，由主任务使用 `--write` 和多个 `--completed 来源ID=退出码` 生成归档。例如仅登记一个确实已经退出 0 的 HTTP 检查：

```powershell
python docs/verification/2026-09-12-fullstack/archive.py --write --completed http-first=0
```

这只是命令格式示例，不代表本目录已经执行过该归档。失败退出码也应如实登记，不能为了生成报告填写 0。原始 JSONL 被其他进程写入时应等待；文件结尾看似有 PASS 或大小暂时不变都不能证明整个生产命令结束。

每次 `--write` 只发布本次显式列出的已完成来源；其他必需来源标记为未记录，`draft=true`。最终归档时把本轮确认完成的来源一起列入命令，避免把上一次结果与新一轮代码混合。脚本不会推导一个“整个项目 PASS”；业务、重启、维护重建、浏览器和教程验收仍需逐项解释覆盖范围。

`linux-integration-race` 与原 `docker-build` 属于历史来源，不承担最终必需门禁。应同时保留并准确传入 `--completed linux-integration-race=1`，其结果仍是 failed。最终宿主集成来源是 `windows-integration`，最终镜像来源是 `docker-build-release`；不能把这两项正在运行的结果提前填写退出 0。

## 结果与原始资料

- 公开输出为本目录 `results.json`。Go 逐测试记录仅有 `package/test/action/elapsed`；HTTP 和补充证明仅保留具名检查及布尔值，不公开 `detail`、页面状态、cookie、token、凭证、原始错误正文或 API 完整响应。
- 只读取代码中列出的文件名，不扫描整个 `.cache/fullstack`，也不读取任何 codes、token 或 `.env` 文件。
- 原始允许来源保存到 `.local/archives/fullstack-20260912/<SHA256>/<文件名>`，复制后再核对 SHA-256。相同文件名的新运行使用不同哈希目录，不覆盖旧原文。**整个 `.local` 归档只供本机排错，不能提交公开仓库。**
- 集成门禁脚本只通过 AST 读取字面量 `REQUIRED` 集合，不执行该 Python 文件；同时私有归档门禁原文与哈希。它证明归档时采用了哪份门禁，不伪造测试启动时的版本来源。
- 原始记录未证明精确被测 Git SHA 时，`exactTestedCommit` 保持 null。`archiveRepositoryHead` 只是生成归档时的 HEAD，不能当作测试时版本。最后代码提交、CI 运行与镜像来源由主任务补充独立关联证据。
- 更新公开结果前，上一份 `results.json` 会保存在私有哈希目录，避免丢失历史失败。生成时如缺少文件、JSON 不完整或结果自相矛盾，应明确失败。

## 重启、重建与浏览器补充格式

允许的最小格式如下；例子故意使用 `passed: false`，不是实际验收结果：

```json
{
  "schemaVersion": 1,
  "runId": "replace-with-actual-run-id",
  "passed": false,
  "checks": [
    {"name": "replace-with-actual-check-name", "passed": false}
  ]
}
```

检查名称使用小写字母、数字与短横线，不能重复或为空。由实际执行者根据真实操作填写，原文件还可保留必要本地诊断，但公开提取不保留任意附加字段。浏览器截图应另行确认没有访问码或用户凭据后再决定是否发布，不能把截图生成等同于交互验收。

## 当前结论边界

三批 HTTP 原始记录分别为十组检查全部通过；Windows 隔离集成退出 0，顶层 168 PASS、3 helper SKIP、0 FAIL、19 项门禁通过；最终 release 构建、实际启动及九项保留检查均退出 0。这些完成通知已被主任务确认，并正式归档。镜像能构建、服务能启动与浏览器可用分别记录，具体边界见验收报告。

本地 `linux-integration-race` 已实际退出 1。执行方记录的问题包括容器缺 Docker CLI、故障 Redis 固定回环端口保护、测试工具的 Compose 模式默认值，以及网络断言在修正前已经编译。归档保留每项实际失败，不将原因说明转换成 PASS。Linux 普通 race、宿主 Windows 真实集成，以及最终云端 Linux 真实依赖 race 是不同运行；云端结果必须另附准确提交 SHA、运行地址和已完成状态，目前不从这些本地文件推断。

浏览器证明来自真实人工式点击、可访问性状态、截图与尺寸观察，不是自动化浏览器回归套件。记录当时搜索异常含英文诊断、gRPC 重连退避后需要再次查询。权限证明检查特定 UID 对分离凭据文件的访问边界，不等于完成通用安全审计。
