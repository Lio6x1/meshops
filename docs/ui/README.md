# MeshOps 网页控制台

当前前端是 [web/](../../web/package.json) 中的 Vue 3 + TypeScript + Vite + Element Plus 应用，通过 HTTP/SSE 网关连接真实后端。已删除被成品替代的离线假数据原型，不再维护第二套界面。

- 先看效果与启动：[完整前后端手册](../run-fullstack.md)。
- 页面功能与边界：[当前界面说明](design.md)。
- 首次管理员设置、个人密码和账号恢复：[个人账号手册](../operations/accounts.md)。
- 六类数量、断网补传和地图：[模拟器与地图](../simulation-map.md)。
- 从零实现：[Z11 网关](../learning/from-zero/lessons/z11.md) → [Z12 Vue](../learning/from-zero/lessons/z12.md) → [Z13 部署](../learning/from-zero/lessons/z13.md)。
- 已有证据：[全栈发布](../verification/2026-09-12-fullstack/release.md)、[混合场景](../verification/2026-09-12-motion-mixed/README.md)。

网页中的实体和执行方仍是模拟对象，但状态、任务、分发和搜索经过实际后端与存储链路。页面不能替代后端授权、状态机或可靠性测试。

完成启动与迁移后，Docker 模式在项目根目录运行 `./scripts/account-admin.ps1 -Action Setup -Username admin`，本地学习模式加 `-Mode Local`；在 PowerShell 7 中按提示安全输入自己设置的密码。网页只接受个人用户名和密码。管理员通过“账号管理”创建操作员；新建或重置的操作员先改密，再重新登录进入业务页。账号与密码哈希保存在 MySQL，普通重启保留账号但需要重新登录；重复 Setup 不会重置密码。
