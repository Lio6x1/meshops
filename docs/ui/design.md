# MeshOps 园区协同控制台设计

## 1. 交付边界与使用者

交付内容是桌面控制台设计和可交互离线原型，供评审现有 MeshOps 业务如何呈现。原型使用固定的 2026-09-12 14:32:10 +08:00 演示时钟与内存数据；它没有认证、HTTP 请求、RPC、设备控制、浏览器存储、自动处置或部署逻辑。

操作员负责查看实体、查询与创建巡检任务、提交取消意图、查询已知下发记录。管理员继承这些能力，并可查询分发器状态和人工重试最新有效死信。角色范围对齐 `internal/tasks` 与 `internal/search` 的实际授权，不新增一个后端尚不存在的“观察者”身份。

浏览器当前不能直接使用此原型访问后端。正式接入需要新的 BFF 和会话体系；本文中的 BFF 能力均为待实现设计，不表示已有 API。

## 2. 信息架构与视觉

| 导航 | 主问题 | 主交互 |
|---|---|---|
| 实体总览 | 现在有哪些实体，哪些需要关注？ | 示例 KPI、示意园区、关注项、实体清单 |
| 实体资源 | 这个实体当前是什么状态？ | 类型与名称/ID 筛选、快照、类型字段、历史样本 |
| 任务中心 | 一次业务执行走到了哪里？ | 创建、列表筛选、详情、状态记录、取消意图 |
| 任务检索 | 哪些任务包含所需线索？ | 关键词、状态过滤、连续游标翻页、打开权威详情 |
| 可靠分发 | 哪次传输失败，是否需要人工干预？ | 按已知任务查询、最新尝试、管理员带原因重试 |
| 运行状态 | 分发器采样状态是否需要关注？ | 三项现有指标、采样时间、权限与失败状态 |

浅色背景承载白色内容卡片；蓝色表示操作与执行能力，青色表示新鲜状态，琥珀色表示过期或待确认，灰色表示只读和未知。状态同时有文字，颜色不是唯一识别方式。主界面保留充足留白，数字与单位分层，诊断身份不喧宾夺主。

园区图是 CSS 构造的静态示意布局，仅帮助找到六类实体入口。点位不是实际经纬度映射，不提供地图服务、真实轨迹、路径规划、围栏判断或控制算法。

1440px 使用完整侧栏、四列 KPI、园区/关注项双列；768px 保留图标侧栏并将内容堆叠；390px 改为顶部紧凑导航、两列 KPI、单列表单。宽表格在独立容器横向滚动，不挤压页面；全局评审面板始终可达。

## 3. 实体模型、能力与新鲜度

六种 `entity_type`：`person`、`drone`、`vehicle`、`robot`、`sensor`、`facility`。仅前四种可以由注册绑定提供 `inspect`。名称与类型不能自行授予执行权限；创建任务仍须由服务端验证 `executor_id`、注册支持任务、最新快照与可用状态。

| 类型 | 详情中保留的专属信息 | 巡检 |
|---|---|---|
| person | `person.on_duty`、availability、skills；没有 power 时不补电量 | 可由注册授权 |
| drone | location 可选 altitude/accuracy；velocity 可选 heading/vertical_speed；power 电量 | 可由注册授权 |
| vehicle | `vehicle.load_kg` 可选载重、availability、速度 | 可由注册授权 |
| robot | `robot.fault_codes`、availability、power | 可由注册授权 |
| sensor | reading 可选读数、unit、measured_at、health | 仅状态 |
| facility | facility_type、is_open、occupied_slots/total_slots | 仅状态 |

可选组件缺失与组件内 optional 标量未提供都必须保留缺失语义。不能把未知电量显示成 0%，不能把缺失 altitude 显示成 0m；不存在的 person 不能推导成“不在岗”。实际存在的 `false`、数值 0、空 fault_codes 列表可以显示为事实。普通 proto3 非 optional 标量不能额外发明缺失标志。

列表显示实体状态、快照新鲜度和最后观察时间；详情补充 `source_id`、`source_generation`、`version`、`expires_at`、执行器绑定。UI 通过 `expires_at` 和服务器校准时间判断过期，避免只按最后一次收到浏览器消息判断新鲜。

连接状态与实体状态分开：订阅断开并不证明实体离线；断开时保留最后快照并明确标记。重新连接建立新订阅，以新的 `sync_id` 重新初始化，等待 `SNAPSHOT_END`；处理 DELETE、HEARTBEAT 和 `view_generation` 切换。V1 每次订阅必须提供 1–100 个明确、授权的实体 ID；`snapshot_version` 不能作为跨实体断点游标。正式列表超过 100 项时仅订阅当前可视页或由 BFF 分组受控汇总。

历史页面使用 `ListHistorySamples`，展示采样时间、发生时间及 sample_reason。它是有界采样而非完整事件档案，不能承诺还原全部运动轨迹。原型详情只有明确标记的静态样本。

## 4. 任务创建与取消

创建表单仅允许 `inspect`，参数为 `duration_seconds`（1–60 整数）和可选 `note`（最多 256 UTF-8 字节）。当前 `NormalizePriority` 仅接受请求值 0 或 5，并统一返回 5；因此表单固定显示 5，不提供 1–10 的可选范围，即使 Proto 注释描述了更宽范围。deadline 必须在当前时刻之后的 24 小时内。原型以固定演示时钟检验时间。正式页须采用服务端校准时间，并保留服务端最终验证。

新草稿生成 `idempotency_key`，提交期间固定该键及规范化后的请求。网络中断或结果不确定时，不自动换键创建；重试完全相同的请求。只有明确成功、明确放弃或用户新建草稿，才能使用新键。响应 `ALREADY_EXISTS` 且参数冲突时提示冲突，不静默换键。原型仅展示草稿键及成功路径，不伪装已经实现不确定响应恢复。

`CREATED → DISPATCH_PENDING → DISPATCHED → ACKED → EXECUTING` 是常见业务过程，并非前端强制的唯一逐步顺序；例如真实执行器 ACK 可以先于分发器的 DISPATCHED 状态写入。前端以 `GetTask` 与状态版本为准，不因为收到重放而退回旧状态。

终态为 SUCCEEDED、FAILED、CANCELLED、TIMED_OUT、REJECTED。终态不显示普通取消入口。Task detail 显示状态、status_version、execution_key、执行器、结果/失败原因，并通过 `GetTaskHistory` 展示持久化状态变化。

点击取消先填写原因（1–1024 UTF-8 字节），调用 `CancelTask`。响应中的 `cancel_requested=true` 表示意图已接受，不证明设备已停止。页面保留原状态，单独显示“已请求取消 · 等待确认”，然后重新查询 Task。只有权威状态变为 CANCELLED 才显示“已取消”。终态之后到达的结果可被后端作为 LATE_RESULT 审计，前端不能拿晚到结果替换已显示的权威终态。

原型详情的“仅评审：切换状态样本”可以切换各类外观；它不是实际业务操作，不模拟合法状态迁移，也不会调用 `ReportTaskStatus`。这个 RPC 只允许 executor/dispatcher_service，不能开放给浏览器操作员。

模拟结果 `effect_count=1` 仅描述本地 bbolt 中的一次确定性模拟结果提交，不表示任意真实设备动作具有恰好一次语义。

## 5. 分发与人工死信处理

分别呈现 `task.status`（业务事实）、`delivery_status`（传输状态）、`attempt`（尝试序号）、`dispatch_id`、`command_id` 与 `execution_key`。`execution_key` 在重试间保持不变；`dispatch_id` 属于一次传输尝试，不能用作新的业务幂等键。

现有 `GetDispatch(task_id, dispatch_id?)` 可查询最新或指定记录，但没有列出全部尝试的集合 RPC。原型的多次尝试时间线被标记为“设计示例 / 待补集合接口”。正式第一阶段只能显示查询得到的单条记录；不能通过猜测 attempt 序号冒充完整记录集。全量死信列表也需要新的授权查询能力。

`RetryDLQ` 仅管理员可调用，必须填写原因。前端只提供用户可解释的预检查，服务端仍需校验最新尝试确实为有效死信、截止时间未过、业务未终止、未被取消命令覆盖、当前轮次未重复重试。`accepted=true` 意味着新传输轮次已持久化，不保证已下发或执行成功。业务任务与 execution_key 不变。

## 6. 搜索与游标

`SearchTasks` 是只读、最终一致的投影。搜索结果显示任务、状态、更新时间和命中线索；打开详情后通过 `GetTask` 读取权威状态，再允许操作。搜索落后不视作 Task 数据损坏。

支持 proto 中实际提供的 keyword、status、target_entity_id、created_from（含边界）、created_before（不含边界）、page_size 和 page_token。原型只交互实现关键词/状态，实体/时间过滤属于正式设计项，不声称已经做成。

结果不提供总命中数，因此不显示伪造 total、任意跳页或末页按钮。下一页使用不透明游标，条件变化清除旧游标；若服务端以 FAILED_PRECONDITION 表示搜索快照过期，保留筛选并从第一页重查。原型每页 2 条仅便于评审，正式默认 20，允许 1–100。

普通任务列表的 `ListTasks` 游标与搜索游标不同，不能互换；普通任务列表支持实体/状态过滤和其自身的分页约束。历史样本游标也独立绑定查询条件。

## 7. 已有契约与需要补充的浏览器接口

| 页面能力 | 已有 RPC | 接入限制 / 待补充 |
|---|---|---|
| 授权实体清单与概览统计 | **无全量枚举 RPC** | 新增服务端授权 inventory 能力；默认只基于授权 ID，不暴露完整 registry 或其他租户 |
| 快照、详情 | Entity.GetSnapshot / BatchGetSnapshots | 浏览器 BFF 将受权 ID 转为 RPC；不能接受客户端自报 tenant 授权 |
| 实体实时变化 | Entity.Subscribe | BFF 转为受权 SSE/WS 或其他浏览器兼容通道；边界、背压、取消与重同步均需实现 |
| 历史样本 | Entity.ListHistorySamples | BFF 传递有界时间范围与不透明游标；不承诺全量历史 |
| 创建、列表、详情、取消、历史 | Task.CreateTask / ListTasks / GetTask / CancelTask / GetTaskHistory | BFF 逐操作授权；前端不开放执行状态上报 |
| 搜索 | Search.SearchTasks | operator/admin；只读最终一致查询；操作前重读 GetTask |
| 最新/指定尝试 | Dispatcher.GetDispatch | operator/admin 可读；无尝试或死信集合接口 |
| 人工重试 | Dispatcher.RetryDLQ | admin；原因、审计和明确的重试结果 |
| 当前运行指标 | Dispatcher.GetStatus | admin；仅 consumer_lag、active_tasks、dlq_count、as_of |
| 队列趋势、服务健康、搜索投影进度、告警 | **没有对应统一 RPC** | 新的授权运维数据源；本原型不把不存在的指标伪装成服务输出 |

以上不是现有 HTTP 路由清单。建议 BFF 通过安全、HttpOnly、SameSite 会话 cookie 识别浏览器用户，并实现 CSRF 防护、同源限制及操作审计。tenant、role、允许的 entity IDs 从服务端可信身份确定。后端 bearer 凭据留在受控服务端，不能写入 JavaScript、HTML、URL、localStorage/sessionStorage、诊断界面或操作表单。BFF 调用执行服务时还须保持用户级授权边界，不能用一个管理员服务凭据替所有浏览器用户绕过权限。

## 8. 状态、无障碍与验收

加载中显示骨架与文字，不能把尚未收到的数据渲染为 0。空结果说明筛选为空，允许清除条件。读取错误显示语义与重试入口；过期分页专门提示重新开始。订阅断开保留最近快照、标注不可靠，并阻止依赖实时状态的操作。未知字段与旧样本须在读取错误后继续保持其语义。

导航使用 button 与 `aria-current`；输入有可见 label；主要内容有跳转链接；对话框使用原生 dialog，支持 Escape、焦点限制与关闭按钮；状态反馈使用 aria-live。危险业务动作先显示原因表单，原型内每个动作明确标为模拟。所有文字插入均转义用户输入；没有网络、CDN、动态代码求值或浏览器存储。

验收重点：六类详情可达；只有四种注册类型具备 inspect，当前忙碌/过期实体不可选；取消意图不等于终态；管理员权限生效；重试不新建业务执行键；搜索分页过期可恢复；加载/空/错误/断连可切换；1440/768/390 宽度和键盘操作均可评审。

## 9. 依据

契约来自当前工作树：`proto/common/v1/entity.proto`、`proto/common/v1/task.proto`、`proto/entity/v1/entity.proto`、`proto/task/v1/task.proto`、`proto/search/v1/search.proto`、`proto/dispatcher/v1/dispatcher.proto`。身份限制与错误语义同时核对了 `internal/tasks/service.go`、`internal/tasks/queries.go`、`internal/tasks/dispatcher.go`、`internal/search/service.go` 和 `internal/state/history.go`；六类 fixture 和模拟器注册绑定来自 `testdata/sources` 与 `configs/simulation.yaml`。
