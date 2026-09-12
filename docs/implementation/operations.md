# 运行配置、身份与操作入口

本页对应当前根工程，保留 O01—O05 编号供契约引用。完整可复制命令集中在 [根 README](../../README.md) 与 [前后端启动手册](../run-fullstack.md)，避免维护两套会漂移的命令表。

## O01 配置与加载

业务入口位于 `cmd/ingest`、`cmd/entity`、`cmd/task`、`cmd/dispatcher`、`cmd/search`；配置位于 `configs/<service>.yaml`，通过 `-f` 选择。共享加载与校验实现位于 [internal/platform/config.go](../../internal/platform/config.go)。Manifest 相对 YAML 所在目录解析；连接地址的环境覆盖仅限加载器明确支持的字段，不存在任意字段通用覆盖承诺。

配置只保存凭证环境变量名，真实值由初始化和环境脚本提供。当前字段、默认值与校验以 [配置类型](../../internal/platform/types.go) 和实际 YAML 为准。不要将旧 `app/*/etc` 或 `internal/runtimeconfig` 路径复制进新工程。

本地 CLI 使用 loopback 地址；完整 Compose 使用专用网络服务名，只有网页入口映射至宿主 loopback。浏览器网关由 [cmd/web-gateway](../../cmd/web-gateway/main.go) 装配；HTTP 映射见 [proto/http.yaml](../../proto/http.yaml)。明文演示网络不代表已有生产 TLS/mTLS。

## O02 身份与授权

[simulation.yaml](../../configs/simulation.yaml) 描述来源、实体、执行方与 inspect 目录。六类来源凭证与四类执行方凭证分离，每个实体只有一个权威来源。身份派生租户与角色，业务层进一步核对来源、实体和执行方绑定；不能信任请求自己声明的租户或能力。

机器令牌用于来源、执行方及内部服务；随机令牌的 SHA-256 查找键不是用户密码加密方案。网页使用服务端会话、角色和写请求 CSRF 校验，浏览器不接收机器令牌；这仍不等于用户注册、密码找回或生产账号系统。实际授权见 [认证实现](../../internal/platform/auth.go)、[网页网关](../../internal/web/server.go) 与 [会话实现](../../internal/web/session.go)。

## O03 初始化、启动与恢复

先体验成品，使用 [Docker 启动步骤](../run-fullstack.md)；逐模块学习，使用 [课程阶段运行手册](../learning/from-zero/CHECKPOINTS.md)。本地 CLI 初始化、启动、停止与搜索单独引导均在根 README，不混用两套环境的凭证、数据库或端口。

MySQL 迁移位于 `migrations/`，实际来源导入及 topic 初始化由现行脚本调用。不要手工照旧计划创建不完整主题或用删除数据卷修复迁移。搜索引导须检查完成标记与 CDC 边界；恢复按 [排错入口](../troubleshooting/README.md) 操作。

本地 `.local/` 保存凭证、进程与维护状态；`data/` 保存 CLI 网关和执行器的 bbolt 数据；Docker 模式使用专用卷。Stop/Down 与 Reset 的数据语义不同，详见启动手册。重放时保留原来源的序列与实体版本，不能用一个新空 bbolt 文件为已有实体重置版本。

## O04 CLI 与浏览器输出

`opctl` 成功向 stdout 输出 JSON：RPC 响应使用 Protobuf JSON，seed 与订阅汇总使用工具定义的普通 JSON；错误/诊断向 stderr 输出。退出码 0 为成功、1 为运行或 RPC 失败、2 为用法错误。流式结果按帧输出，不能把一整条流当成单个 JSON 对象。任务 ID 使用实际创建响应，不能用虚构 ID 证明查询和执行成功。

状态快照、订阅、历史、任务创建/取消/查询、分发查询/人工重试和任务搜索的命令见根 README。浏览器使用同源 HTTP/JSON 和 SSE；取消请求不等于设备已经停止，搜索结果不替代任务事实，动作前核对当前任务状态。

模拟来源支持暂停、断网缓存与恢复上报；网页每类 0—5 个。独立压测不受网页数量上限约束，见 [模拟场景与地图](../simulation-map.md)。具体 flag 与错误以当前 CLI 解析器为准，不把已删除的占位程序说明当成可执行接口。

## O05 修改与验证

修改配置或接口时同步相关 `cmd/`、`internal/`、`configs/`、Proto/生成文件、脚本、启动说明及受影响课程；禁止手改生成文件。遵循 [课程维护顺序](../learning/from-zero/maintenance/README.md)。

普通测试、真实依赖 integration、故障演练、压测、浏览器交互与课程复制分别验证。纯导航整理只检查链接、指纹及差异，不重复停机演练；业务变更按风险执行实际测试。A01—A28 定义见 [验收矩阵](acceptance.md)，搜索及前后端的额外证据见 [验证边界](../production-readiness-checklist.md)。
