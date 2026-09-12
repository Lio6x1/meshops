# 完整前后端成品与学习部署设计

用户已批准：先交付可以独立运行的完整成品，再以成品为目标从空目录学习；不以静态原型或最小页面替代完整业务展示。原后端保留，范围为六类实体、四类 inspect 执行方、历史、可靠分发及任务搜索。

## 成品与边界

参考工程位于 meshops，学生工程位于同级 meshops-course-lab。Vue 3 + TypeScript + Vite + Element Plus 实现实体、任务、搜索、分发与运行状态页面。默认中文界面，清楚标注模拟来源；页面数据来自后端。离线原型作为设计历史保留链接，不参与运行。

浏览器用同源 HTTP/JSON 和 SSE。新增 Go web-gateway 使用 grpc-gateway 生成选定的一元接口，不开放来源上报或执行状态回报。HTTP 映射放在外部 proto/http.yaml，以免修改既有线协议；教程同时解释 google.api.http 注解的等价含义。现有 Entity/Task/Dispatcher/Search 继续通过 gRPC 调用。

完整演示是单机 Docker Compose，独立项目与数据卷，宿主仅映射 loopback 网页入口。学习模式沿用依赖容器和 IDE 运行 Go，Vite 用同源代理接入。初始化是可重入操作，创建随机凭证、执行迁移/seed、建立搜索快照与 CDC 起点，再启动 Canal/Search。重启不删除卷或凭证。所有容器运行同一套源码，只有配置不同。

## 浏览器接口约定

JSON 采用 Protobuf 默认 lowerCamelCase，int64 保留字符串，enum 使用完整名称；不丢失 optional 字段。普通 RPC 失败返回 {code,message}，code 为 gRPC 数字码。页面不把缺值视作零，也不把取消意图当成终态。

| HTTP | 内容 |
| --- | --- |
| POST /api/session | {role: operator或admin, accessCode}；随机演示访问码，不是后端机器令牌 |
| GET /api/session | 当前 {role,tenantId,actorId,csrfToken,expiresAt}；无登录返回 401 |
| DELETE /api/session | 注销 |
| GET /api/v1/entities | {entities:[{entityId,entityType,executorId,supportedTasks}]}，仅可信身份所属租户 |
| GET /api/v1/entities/{entity_id} | GetSnapshot |
| GET /api/v1/entities/{entity_id}/history | ListHistorySamples；start_time/end_time/page_size/page_token |
| GET /api/v1/entities/stream?entity_ids=a,b | SSE event:update，data 为完整 EntityUpdate；event:error 为有界错误。重连建立新 sync_id，SNAPSHOT_END 才结束初始化 |
| POST /api/v1/tasks | CreateTask；请求幂等键在不确定响应时复用 |
| GET /api/v1/tasks | ListTasks；target_entity_id/status/page_size/page_token |
| GET /api/v1/tasks/{task_id} | GetTask |
| POST /api/v1/tasks/{task_id}/cancel | CancelTask；body {reason} |
| GET /api/v1/tasks/{task_id}/history | GetTaskHistory |
| GET /api/v1/tasks/{task_id}/dispatch | GetDispatch，支持 dispatch_id 查询 |
| POST /api/v1/tasks/{task_id}/retry | RetryDLQ；body {reason}，仅 admin |
| GET /api/v1/dispatcher/status | GetStatus，仅 admin |
| GET /api/v1/search/tasks | SearchTasks；keyword/status/target_entity_id/created_from/created_before/page_size/page_token |

会话只存服务端内存，随机会话 ID 放 HttpOnly、SameSite=Strict cookie，固定过期时间；服务器重启要求重登。写请求校验 Origin 与 X-CSRF-Token；登录也检查可信 Origin 和有界速率。浏览器收到的访问码仅用于登录，不存 localStorage；后端机器令牌只存在服务端。随机演示访问码不是用户密码存储体系，不实现注册/找回密码。Gateway 按已认证角色选择同租户 operator/admin 凭证，服务端仍执行现有权限与对象归属检查。实体列表来自可信 Registry，不接受客户端指定 tenant。

SSE 限制实体数量和连接数量、取消上游并限制慢客户端写入；会话失效时断开。禁用代理缓冲；前端分别显示订阅连接状态、快照过期及实体业务状态。已有 Proto 返回缺省字段时按其语义处理，64 位版本不转换为 JS Number。

## 验收

网关回归覆盖未登录、跨 Origin、CSRF、operator 调 admin、伪造租户/凭据、不暴露执行回报、会话过期、SSE 取消及真实 gRPC 转换。前端测试覆盖查询错误、空数据、过期、取消意图、幂等重试、搜索游标失效和订阅初始化。Compose 从干净专用卷启动、六类实体更新、四执行方任务、取消、历史、搜索及人工重试都有真实运行证据；重启保留业务数据。只对专用测试环境执行故障操作。

正式交付包括完整源码、锁文件、镜像构建、双模式启动、演示/停止/诊断手册和 Z10 后完整答案课程。现有 Z01—Z10 发布指纹保持原阶段语义；新增能力另立阶段，不向早期课程灌入前端。证据先脱敏归档，再检查目录链接与占用并清理缓存/旧工作树。许可证最后确认，topics 使用 go/grpc/kafka/redis/outbox/cdc/vue/docker-compose。pending 优化先测积压，不增加无依据的队列框架。
