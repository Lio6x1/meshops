# 从零课程材料导航

**开始学习请打开 [课程目录](lessons/README.md)，从 Z00 进入。** 不需要按本目录的文件排列顺序逐个阅读。

| 你要做什么 | 打开哪里 |
| --- | --- |
| 从零学习与搭建 | [课程目录](lessons/README.md) |
| 查本阶段要创建、替换和移除哪些文件 | 课程表中的“完整文件答案”；Z04 起配合 [分步操作](lessons/steps/README.md) |
| 核对启动方式和环境切换 | [阶段运行手册](CHECKPOINTS.md) |
| 查错误和日志 | [排错实操](lessons/debugging.md) |
| 直接运行最终示例 | [完整参考工程](../../../README.md) |
| 修改课程、交给其他 AI 继续维护 | [维护说明](maintenance/README.md) |

## 目录分别存什么

```text
from-zero/
├── lessons/                 学生讲义：z00—z10.md
│   ├── steps/               Z04—Z10 的分步操作
│   └── files/               各阶段完整文件答案
├── exercises/              讲义明确要求运行的辅助练习
├── （最终工程位于仓库根目录，不在教材内另存一份）
├── starter/z01—z03/         前三个阶段的完整源码快照
├── state-stages/z04—z06/    状态链路的阶段源码快照
├── business-stages/z07—z09/ 任务、历史与原验收阶段的固定源码快照
├── substeps/               少量阶段内过渡文件
├── verification/           教程复制、阶段运行与排错的验证记录
└── maintenance/            教师与 AI 维护说明
```

仓库根目录 `verification/` 保存业务工程的验收证据；本目录 `verification/` 保存教程本身的验证证据。二者不是重复验收同一件事。

## 为什么有相似代码

各阶段是项目在不同学习时刻的完整答案，不是同时部署的多个服务。最终实现集中在仓库根目录；阶段快照保留该阶段已经学到的能力。`lessons/files/` 再把需要新增或替换的源码展示成可复制代码块。

维护时从阶段源码生成展示代码并核对，不能把代码块和阶段文件各改一遍而不检查一致性。`.bin` 协议基线必须复制原文件，不能当文本粘贴。

## 自己写代码的位置

学生工程约定为 `D:\job\golang\projects\meshops-course-lab`，由学习步骤创建。它不在教材目录内。参考工程位于 `D:\job\golang\projects\meshops`；旧 `app/` 框架已删除，根目录即新版最终工程。

手工复制路线和 `apply-checkpoint.ps1` 管理路线二选一；默认跟随手工路线，不要在同一个学习工程混用。生成讲义、重建检查点和验收驱动等脚本属于维护工具，学生不用逐个运行。

## 当前交付范围

六类状态接入、查询/订阅、历史、四类实体的 inspect、可靠分发与恢复已在参考工程实现。各阶段已有完整文件答案及连续验证；教程的全部说明和操作衔接仍待最终复核。

任务搜索（MySQL → Canal → Kafka → Elasticsearch）已在根工程与 Z10 实现，并配有完整答案、分步操作及搜索引导/恢复验证。Z09 保留加入搜索之前的阶段能力；阅读阶段材料时以阶段编号区分，不把 Z09 的边界理解成根工程缺失。

## PowerShell 命令约定

`powershell -ExecutionPolicy Bypass -File ...` 为新进程执行脚本；设置只影响该次进程。`. ./scripts/env.ps1`（点与路径之间有空格）将环境变量加载进当前终端，两者用途不同。Z04 起需要当前终端保留数据库和 RPC 环境，所以使用点调用。

如果可信课程脚本被本机执行策略阻止，先运行 `Get-ExecutionPolicy -List` 查看限制；普通个人学习环境可在当前终端执行 `Set-ExecutionPolicy -Scope Process -ExecutionPolicy Bypass` 后继续。它不会永久修改系统策略，关闭终端即失效；企业组策略限制须按企业规则处理。不要将秘密环境变量输出到截图或日志。

修改协议所需工具见根工程 [协议工具与基线说明](../../../testdata/proto/README.md)。显式完整依赖验收使用 `./scripts/test.ps1 -Integration`；支持 CGO/C 编译器的环境可加 `-Race`，Linux CI 对全部依赖路径执行 race。普通无依赖测试中的 Skip 不代表集成能力已验收。
