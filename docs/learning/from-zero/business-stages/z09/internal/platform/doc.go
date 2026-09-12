// Package platform 提供进程之间共享的配置、可信身份、连接和签名游标规则。
//
// Manifest 是管理员预先配置的权威绑定。来源上报不能改变 tenant、来源代次、
// 实体能力或执行方；RPC 拦截器将机器令牌解析为 Principal 后放入 context，
// 业务层仍需检查实体/任务归属。通过鉴权不等于有权操作每一个对象。
//
// 本地演示使用随机机器令牌及 loopback 明文连接，不包含用户密码存储。
// Hash 的 SHA-256 用于高熵令牌查找、内容指纹或幂等摘要，不是密码 KDF。
// Registry 加载后按只读对象共享；运行期间修改其 map 会破坏并发读安全。
package platform
