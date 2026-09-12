# Linux race 补充验证与远端 CI 状态

## 实际结果

使用本机已有 `golang:1.25.10` Linux 镜像、`CGO_ENABLED=1`，没有修改业务 Go 代码。

| 检查 | 结果 | 原始证据 |
| --- | --- | --- |
| 整个根工程 `go test -race ./...` | 退出 0；84 个顶层测试 PASS，12 个依赖类测试 SKIP，0 FAIL；未发现数据竞争 | `.cache/race-verification-20260912/unit-race-current-cache.jsonl` 及同名 stderr 文件 |
| 搜索真实依赖 `go test -race -tags integration ./internal/search` | 退出 0；19 个顶层测试 PASS，0 SKIP，0 FAIL；未发现数据竞争 | `.cache/race-verification-20260912/search-integration-race.jsonl` 及同名 stderr 文件 |
| CI 检查脚本 | 工作流 YAML、内嵌 Python 语法检查通过；接受真实成功报告，拒绝缺少或 SKIP 的必需测试 | `.cache/race-verification-20260912/ci-guard-test.txt` |

上述数字只计顶层测试，不叠加子测试，也不把没有测试的命令包计成场景。完整工程的普通 race 未设置外部依赖，因此不能据此说全部集成路径已做 race；随后单独为搜索设置了真实 MySQL、Kafka、ES。原 Redis/状态链路的 12 个依赖类测试没有在本次 race 中执行，保留此前的其他验收口径。

搜索实际执行并通过了以下三个关键集成测试：

- `TestRealElasticsearchVersionProjection`：真实 ES 外部版本、重复/乱序、冲突和查询投影。
- `TestSnapshotEstablishesReadViewBeforeReleasingLock`：真实 MySQL 一致性视图和释放锁后的并发变更。
- `TestCDCRebuildStartsAfterOldHistory`：真实 Kafka 重建起点，活动消费组拒绝重置及边界变化检查。

## 环境与失败记录

首次使用较早的 `.cache/gopath` 离线依赖缓存时，缺少当前 MySQL driver、kafka-go 和 bbolt 版本，因 `GOPROXY=off` 在编译阶段失败。该次不是业务竞态失败，日志保留为 `unit-race.jsonl` 和 `unit-race.stderr.txt`。随后改用本机已有的 `D:/my_projects/pkg/mod` 当前依赖缓存，只读挂载后通过，没有下载或升级依赖。

源码和依赖目录均只读挂载；Linux 构建缓存写入 `.cache/go-build-linux-race`。测试的临时数据库、Kafka topic、ES 索引由测试生成独立名称并清理。没有停止课程依赖或修改课程任务表，测试结束后容器自动移除。

真实搜索测试共享 ES 容器的网络命名空间：ES 使用 `127.0.0.1:9200`，MySQL 使用 Docker 内部 `mysql:3306`，Kafka 使用 `kafka:29092`。这样保留代码对 ES 回环地址的限制，无需放宽为外部 HTTP 地址。

## 当前机器复现

从项目根目录运行，Docker 已启动；下列挂载路径对应本机，其他机器应换成自己的源码与已下载依赖缓存路径。完整普通 race：

```powershell
docker run --rm `
  --mount type=bind,source=D:/job/golang/projects/meshops,target=/src,readonly `
  --mount type=bind,source=D:/my_projects/pkg/mod,target=/go/pkg/mod,readonly `
  --mount type=bind,source=D:/job/golang/projects/meshops/.cache/go-build-linux-race,target=/root/.cache/go-build `
  -e GOTOOLCHAIN=local -e GOPROXY=off -e CGO_ENABLED=1 -e GOMAXPROCS=4 `
  -w /src golang:1.25.10 go test -race -p 2 ./... -count=1 -timeout=10m -json
```

搜索真实依赖 race 要求课程 MySQL/Kafka/ES 已运行：

```powershell
docker run --rm --network container:meshops-course-elasticsearch-1 `
  --mount type=bind,source=D:/job/golang/projects/meshops,target=/src,readonly `
  --mount type=bind,source=D:/my_projects/pkg/mod,target=/go/pkg/mod,readonly `
  --mount type=bind,source=D:/job/golang/projects/meshops/.cache/go-build-linux-race,target=/root/.cache/go-build `
  -e GOTOOLCHAIN=local -e GOPROXY=off -e CGO_ENABLED=1 -e GOMAXPROCS=2 `
  -e 'MESHOPS_TEST_MYSQL_DSN=root:course_local_root@tcp(mysql:3306)/meshops_course?parseTime=true&loc=UTC' `
  -e MESHOPS_TEST_KAFKA_BROKERS=kafka:29092 `
  -e MESHOPS_TEST_ES_ENDPOINT=http://127.0.0.1:9200 `
  -w /src golang:1.25.10 go test -race -tags integration -p 2 ./internal/search -count=1 -timeout=5m -json
```

DSN 中是已经写入课程 Compose 的本机教学密码，不是操作员 token。不要把 `.local/secrets.json` 中的实际机器凭证加入报告。

## CI 已补什么，还缺什么

后续更新：同日发布准备阶段已恢复 GitHub 连接，并补上 Git 换行转换后的教材指纹验证，见[发布前校验](2026-09-12-git-publication.md)。以下保留本次 race 补验结束时的实际状态，不代表后续发布状态。

`.github/workflows/ci.yml` 已增加真实搜索集成 race，并读取 JSON 报告，要求上述三个测试均出现 PASS。测试未发现、环境缺失导致 SKIP 或执行失败都会阻断该项；不再仅凭 `go test` 返回 0 忽略必需测试。

远端仍为 `https://github.com/Lio6x1/meshops.git`。当前本地 HEAD 是 `fc9b98752470afa63ccaf64e3592c08147932f58`，工作区包含尚未提交的工程迁移、业务代码和课程，不能把旧提交的 CI 当成本轮成果已验证。

只读 `git ls-remote` 在沙箱内遇到凭据句柄限制；授权后重试仍无法连接 GitHub 443，约 21 秒超时。因此本轮没有实际运行远端 GitHub Actions，也没有提交、推送代码。后续需恢复远端连接、确认具体提交上传范围，再让当前工作流对新提交执行。远端 CI 的这一项保持未完成，不能用本机 race 结果替代。
