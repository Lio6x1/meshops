# 完整前后端发布回执

功能提交：`28fad4652e4168888cc2af8de3e9a17b4e15bef0`，已快进整合并推送到 `main`。

[GitHub Actions 34695085212](https://github.com/Lio6x1/meshops/actions/runs/34695085212) 的 `browser`、`reference` 两项 job 均为 `completed / success`，[API 结果](cloud-ci.json)已保存。检查包括前端依赖安装、测试和生产构建，1676 条课程源文件指纹，Go build/vet/staticcheck，普通 Linux race，以及连接真实 MySQL/Redis/Kafka/ES 的完整 integration race 和必跑门禁。

本回执与对应清理、CI 证据属于验收后的纯文档提交，不修改功能代码、依赖、构建配置或课程源码指纹。该文档提交使用 `[skip ci]` 避免重复执行相同功能测试；功能验收明确指向上面的提交，不能把跳过当作另一次测试通过。

本机最终 Docker 镜像已启动，并核验原任务状态、执行效果与搜索结果保留。浏览器入口为 `http://localhost:18090`，启动、访问码、停止和排错见[完整手册](../../run-fullstack.md)。Windows 本地 CLI 与 HTTP 网关二进制也已按功能提交重新构建。真实数据源、真实硬件、跨机高可用和生产容量不在此次验收范围。

工程清理先归档原始证据再执行。旧评审/复制目录清理 4,119,914,506 字节；九个旧 Go 构建缓存通过官方 `go clean -cache` 清理 12,377,016,232 字节，合计约 16.5 GB 的逻辑文件大小。当前 Go 缓存、源码、业务卷和证据归档保留，专属临时验收 Docker 项目已清理。逐项字节记录见[清理明细](cleanup.json)，这个统计不代表 Docker Desktop 虚拟磁盘文件会同步缩小。

GitHub 已设置 Go、gRPC、Vue、Kafka、Redis、MySQL、Elasticsearch、Outbox、CDC 等主题。许可证仍等待项目所有者选择，未擅自加入 MIT 或 Apache-2.0；这不影响本机运行与学习。
