# MeshOps z07 完成检查点

这是可独立构建的阶段源码。module 为 example.com/meshops-course。
先停止上一阶段服务，在本目录运行 ./scripts/initialize.ps1、./scripts/start.ps1 -Simulators、./scripts/demo.ps1，结束运行 ./scripts/stop.ps1。
initialize 包含七个程序的构建、001—004迁移和六类来源/四类执行方 seed。

Z07 支持六类当前状态、订阅、持久补传、inspect 任务、取消/超时/DLQ；状态历史从 Z08 加入。
Z08 另支持状态历史抽样与查询。影子恢复、verify 压测和故障工具从 Z09 加入。
本阶段无 --rebuild-view 参数；Z07 无 opctl history 命令，协议中尚未实现的历史 RPC 返回 UNIMPLEMENTED。
迁移先保留完整编号，历史表是 Z08 的预备 schema，Z07 不写入历史样本；任务审计 history 不受影响。

本阶段使用最终专用 meshops-course Compose（MySQL13306、Redis16379、Kafka19092）。
从 Z06 的 meshops-state-lessons 环境升级时停止旧服务，重新 seed 并上报模拟状态；不会自动搬迁旧 Redis 视图。
在同一 learner 目录升级 Z07→Z08→Z09 会保留 .local 凭证、bbolt 文件和 Compose 数据。
这份目录是阶段答案，逐课详细讲义和全课程复制验收分别跟踪，不能据此声称已经学会。