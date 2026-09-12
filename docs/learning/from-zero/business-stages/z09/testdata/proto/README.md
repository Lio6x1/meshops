# 协议基线

`legacy.bin` 是仓库原骨架 `proto/` 的 Buf 描述符快照；`reference-v1.bin` 是独立课程模块首版的协议基线。两者均由 Buf 1.72.0 build 生成，包含描述符，不包含凭证。

首次拆分为独立 module 时有两项明确的 Go 源码接口变化：所有文件的 `go_package` 改为 `example.com/meshops-course/gen/...`；Location 的 altitude/accuracy、Velocity 的 heading/vertical_speed 改为 optional，区分“未知”和零。旧代码不能不改 import/字段访问就直接替换为新模块。字段编号和线格式保持兼容。

验收要求：相对 legacy 的 WIRE 检查通过；FILE 检查报告上述已知变化，不能宣称完全源码兼容。后续修改相对 `reference-v1.bin` 执行 FILE 检查。不要为了让检查通过而自动重建基线。

生成工具锁定为 protoc 36.0-rc1、protoc-gen-go v1.28.1、protoc-gen-go-grpc 1.2.0。这里沿用已验证的课程生成工具，运行项目只需要现有 `gen/`，不要求学员安装预发布 protoc。
