# 模拟器控制与实体地图

在完整参考工程中运行 `./scripts/demo-stack.ps1 -Action Up`，按[账号手册](operations/accounts.md)通过 Setup 设置管理员，再用个人用户名和密码登录 `http://localhost:18090`。管理员可创建操作员，操作员须先完成首次改密并重新登录。两种角色都可使用模拟演示。实体总览和实体资源都有二维地图；左侧“模拟演示”控制后台数据源。无需地图密钥，也不调用外部瓦片服务。

## 先体验

在“模拟演示”中，人员、无人机、地面车辆、巡检机器人、固定传感器和设施各有一个数量输入框。每类允许 **0—5 个**，默认 1 个，总计最多 30 个。输入后点击“应用数量”，等待“实际启用”与目标一致，再进入实体地图；已打开的地图可点“刷新清单”。这是真实注册和上报的实体，不是前端复制的装饰点。

1. 在实体总览查看六类点位，点击无人机点位打开详情。经纬度、电量、版本都来自实际 Entity 服务。点击“查看该实体任务”会带入实体筛选，创建任务时也预选该实体。
2. 打开模拟演示，对无人机点击“暂停采集”。先显示期望模式，再等待实际状态成为“暂停采集”。采集和发送计数停止增长，已有缓存保留。
3. 点击“断网缓存”。模拟器继续写本地 bbolt 队列，上传连接被取消，待补传数量增长。这模拟的是数据上报链路断开，执行任务的进程仍在运行。
4. 回到地图，最后位置保留。超过配置中的 `stale_after: 30s` 后，点位显示灰色虚线；这表示数据过期，不是设备硬件故障的诊断。
5. 回到模拟演示，点击“正常上报”。原队列自动补传，新鲜快照经 Kafka、Redis 和 SSE 到达网页，点位继续移动。
6. 需要整体演示时使用“全部开始上报”“全部暂停采集”。它们逐个提交，部分失败会说明已提交数量，不承诺六个来源同时切换。

## 三种模式的边界

| 模式 | 生成观测 | 上传 | 本地已有缓存 | 任务执行器 |
| --- | --- | --- | --- | --- |
| 正常上报 | 持续 | 持续，自动补传 | 收到有效 ACK 才删除已确认前缀 | 独立运行 |
| 暂停采集 | 暂停 | 暂停 | 保留 | 独立运行 |
| 断网缓存 | 持续 | 断开 | 增长，仍受队列容量上限限制 | 独立运行 |

“进程有心跳”只说明模拟器控制循环活着；上传是否健康要结合发送计数、积压和实体新鲜度。没有心跳时实际模式和计数显示未知，不把期望状态冒充成功。

目标模式持久化在 Redis，重启后仍保留。模拟器心跳 3 秒过期，约每 500ms 读取控制，网页每 1.5 秒刷新。Redis 故障时模拟器暂停采集和上传，恢复后重新读取目标状态。失败处置不是跳过或清空队列。

## 后端如何接入

```text
Vue /simulation
  → 已登录的 HTTP 网关（Origin / CSRF / 租户注册清单校验）
  → Redis simulation 命名空间：目标模式
  → gateway-simulator 读取目标，切换生成器与上传协程
  → Redis：模拟器实际模式、心跳、bbolt 待补传数量
  → 网关查询 → 页面展示

观测数据继续走原链路：模拟源 → bbolt → gRPC Ingest → Kafka → Entity/Redis → SSE → 地图。
```

控制端点是演示辅助 HTTP 接口，不属于实体或任务业务 RPC，没有把浏览器接入执行方回报协议。

- `GET /api/v1/simulation`：返回当前租户来源清单、目标和观测状态。
- `PUT /api/v1/simulation/{source}`：JSON `{"mode":"running"}`、`paused` 或 `offline`，成功返回 **202**，只代表已记录目标。需要会话 cookie、可信 Origin 和 CSRF。
- `PUT /api/v1/simulation/{source}/count`：JSON `{"count":5}`，必须是 0—5 的整数且不超过来源已注册候选数。202 只表示目标数量已持久化，实际启用数量来自模拟器心跳 `activeCount`。
- `MESHOPS_SIMULATION_CONTROL=1`：显式启用；未启用的网关返回 503。Compose、`start-web.ps1` 和 `start.ps1 -Simulators` 已设置。手工启动模拟器时需自己设置此变量，并与网关使用同一个 `MESHOPS_REDIS_ADDR`（本机默认 `127.0.0.1:16379`）。

完整场景预注册每类 5 个候选 ID（如 `drone-001` 至 `drone-005`），启用排序后的前 N 个。模式仍按来源切换，影响这一来源的全部启用实体。四类可执行实体共 20 个绑定到对应执行器；传感器和设施共 10 个仅上报状态，不增加任务能力。

数量和模式独立保存。减少数量停止相应实体的新观测，并从当前清单、地图和创建任务选择中隐藏；不会删除注册、历史快照、已有任务或 bbolt 缓存。此前缓存的数据仍可补传，已有任务仍由独立执行器完成。直接按已注册 ID 查询历史仍然允许。0 表示这一类型不再生成新数据；全部设为 0 时地图显示空场景。

每个来源总计约 2 条观测/秒，按启用 ID 轮流生成，5 个实体时每个约 2.5 秒更新一次。六来源合计约 12 条/秒。本功能是混合实体演示，不能作为一万个混合实体容量测试的结论。

## 地图坐标与学习范围

SVG 底图为绘制的园区示意，建筑位置并非真实测绘。以模拟数据的 `31.23, 121.47` 为中心，将小范围经纬度近似换算为平面米数；地图覆盖约 1000m × 640m。不是通用 GIS，也不计算导航路线。没有坐标、坐标非法或超出区域的实体计入提示，仍可在清单查看，不强行放到中心或边缘。

模拟器在统一格式观测上生成有界运动。人员沿综合楼外围步道、机器人沿运维站外围巡检，无人机作椭圆巡航，车辆沿主路和东南外围道路行驶；传感器与设施固定。路线分别约 50/60/35/65 秒一圈，是便于观察的加速演示，不是设备动力学或导航算法。以事件的 occurred_at 计算位置，增加实体数量不会降低路线速度；同类五个实体分散在不同路线阶段。已有速度组件同步更新为当前模拟路线的速度与朝向。

位置真正写入事件链路，前端不预测下一位置。地图的“最近轨迹”开关显示本页收到的最近 30 秒、最多 20 个位置点，固定实体不画轨迹。重新同步、数据来源代次变化、切换页面会重新积累，长时间无新点后不会用直线连接断档。轨迹只在浏览器内存中，不代替后端历史记录。地图与清单共享一个 SSE 订阅，按当前筛选联动。缩放后可滚动查看，支持按钮键盘操作与减少动画偏好。

## 代码与课程入口

- [模拟器运行循环](../internal/edge/simulation.go)：取消并等待旧上传协程退出后，再发布已应用状态。
- [Redis 控制状态](../internal/simulation/store.go)：目标持久化、心跳 TTL、64 位计数字符串。
- [HTTP 控制接口](../internal/web/simulation.go)：会话后置路由、来源隔离、严格参数、202 语义。
- [地图坐标函数](../web/src/map.ts)和[地图组件](../web/src/components/EntityMap.vue)：过滤有效位置、显示新鲜度、选择实体。
- [控制页面](../web/src/views/Simulation.vue)：目标与实际分开，串行批量请求、过期响应保护、卸载中止请求。
- [Z11](learning/from-zero/lessons/z11.md)学接口，[Z12](learning/from-zero/lessons/z12.md)学页面，[Z13](learning/from-zero/lessons/z13.md)装配可控制的模拟器。最新完整文件答案随这三课一起发布。

## 验证

普通测试：`go test ./internal/web ./internal/edge ./internal/simulation ./cmd/web-gateway`；前端：`./scripts/frontend.ps1 -Action Test` 和 `-Action Build`。Redis 生命周期测试需要 `MESHOPS_TEST_REDIS_ADDR`，只操作唯一的测试键。

真实演示验收脚本 `scripts/test-simulation.py` 使用 Python 3 标准库，连接 `http://127.0.0.1:18090`。先在网页创建操作员并完成首次改密；脚本要求操作员角色，不能使用管理员或尚待改密的账号。在项目根目录的 PowerShell 7 中交互输入该操作员的用户名和密码，凭证只临时传给 Python 子进程：

```powershell
$simulationCredential = Get-Credential -Message '输入已完成首次改密的操作员用户名和密码'
$simulationEvidence = Join-Path 'results' ('simulation-' + [Guid]::NewGuid().ToString('N') + '.json')
try {
    $env:MESHOPS_WEB_OPERATOR_USERNAME = $simulationCredential.UserName
    $env:MESHOPS_WEB_OPERATOR_PASSWORD = $simulationCredential.GetNetworkCredential().Password
    python -B scripts/test-simulation.py $simulationEvidence
    if ($LASTEXITCODE -ne 0) { throw '模拟演示验收未通过，请查看本次结果文件。' }
} finally {
    Remove-Item Env:MESHOPS_WEB_OPERATOR_USERNAME, Env:MESHOPS_WEB_OPERATOR_PASSWORD -ErrorAction SilentlyContinue
    if ($null -ne $simulationCredential) { $simulationCredential.Password.Dispose() }
    Remove-Variable simulationCredential -ErrorAction SilentlyContinue
}
```

输出路径必须不存在。脚本会临时控制无人机来源，验证上报、暂停、离线缓存、过期和恢复，最后恢复运行前的期望模式；不删除数据。不要在另一人正在做同一来源演示时同时运行。密码不写入结果文件，也不应硬编码进命令或打印环境变量；运行期间它存在于当前进程及子进程内存中，结束后上面的 `finally` 清除临时环境变量。

脚本会临时把无人机数量设为 1，并恢复原数量。要验证完整数量场景，重新执行上面的安全输入代码，将 `try` 中的 Python 命令改为 `python -B scripts/test-scene-counts.py $simulationEvidence`，并将结果文件前缀从 `simulation-` 改为 `scene-`。该脚本验证 30 个新鲜快照、四类 `-005` 实体任务成功、数量越界拒绝、缩减后历史保留以及全零空场景；最后逐来源恢复原数量和模式。四条测试任务保留在历史中。

运动验收复用相同安全输入步骤，将 Python 命令改为 `python -B scripts/test-motion.py $simulationEvidence`，结果文件前缀改为 `motion-`。它临时启用六类各 5 个，比较真实 HTTP 快照：20 个移动实体几秒后应有明显位移，10 个固定实体位置应相同；结束恢复原数量与模式。地图轨迹开关、短轨迹和重新同步仍需通过浏览器检查。

已有单实体演示环境重新 Up 时需要增量扩充注册清单，不能通过删除数据库解决。初始化在服务停止的维护阶段进行；只允许增加来源映射中的新 ID，旧实体归属、执行器、凭证和代次必须保持一致。

本地 Go 学习模式升级旧环境时，先停止业务进程，加载自己的原凭证并构建最新版 `opctl`，然后执行 `./bin/opctl.exe seed --manifest configs/simulation.yaml --token-env MESHOPS_ADMIN_TOKEN --allow-entity-expansion`。确认成功后重新启动业务。普通 `initialize.ps1` 和 `seed` 仍严格拒绝修改既有来源映射，扩充必须显式选择这个维护参数；从空目录首次初始化不需要它。Docker `Up` 已在维护阶段使用该参数。
