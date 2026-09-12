# 协议基线

`legacy.bin` 是仓库原骨架 `proto/` 的 Buf 描述符快照；`reference-v1.bin` 是独立课程模块首版的协议基线。两者均由 Buf 1.72.0 build 生成，包含描述符，不包含凭证。

首次拆分为独立 module 时有两项明确的 Go 源码接口变化：所有文件的 `go_package` 改为 `example.com/meshops-course/gen/...`；Location 的 altitude/accuracy、Velocity 的 heading/vertical_speed 改为 optional，区分“未知”和零。旧代码不能不改 import/字段访问就直接替换为新模块。字段编号和线格式保持兼容。

验收要求：相对 legacy 的 WIRE 检查通过；FILE 检查报告上述已知变化，不能宣称完全源码兼容。后续修改相对 `reference-v1.bin` 执行 FILE 检查。不要为了让检查通过而自动重建基线。

生成工具锁定为 protoc 36.0-rc1、protoc-gen-go v1.28.1、protoc-gen-go-grpc 1.2.0。这里沿用已验证的课程生成工具，运行项目只需要现有 `gen/`，不要求学员安装预发布 protoc。

## 需要修改协议或执行精确生成检查时

以下安装只影响当前终端的 PATH；工具版本与运行时依赖版本不是同一概念。不要把下载失败处理成更新协议基线。

1. 从 [Protobuf v36.0-rc1 官方发布页](https://github.com/protocolbuffers/protobuf/releases/tag/v36.0-rc1) 下载适用于 Windows x64 的 protoc 压缩包，解压到自行选定的目录，例如 `D:\tools\protoc-36.0-rc1`。保留完整 `bin` 和 `include` 目录。
2. 从 [Buf v1.72.0 官方发布页](https://github.com/bufbuild/buf/releases/tag/v1.72.0) 下载 Windows x86_64 的可执行程序，命名为 `buf.exe` 放到 `D:\tools\meshops-bin`。
3. 安装锁定的 Go 插件。版本分别见 [protobuf-go v1.28.1](https://github.com/protocolbuffers/protobuf-go/releases/tag/v1.28.1) 与 [protoc-gen-go-grpc v1.2.0](https://github.com/grpc/grpc-go/releases/tag/cmd%2Fprotoc-gen-go-grpc%2Fv1.2.0)。

```powershell
$savedGoBin = $env:GOBIN
try {
    $env:GOBIN = 'D:\tools\meshops-bin'
    New-Item -ItemType Directory -Force $env:GOBIN | Out-Null
    go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.28.1
    if ($LASTEXITCODE -ne 0) { throw 'protoc-gen-go installation failed' }
    go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.2.0
    if ($LASTEXITCODE -ne 0) { throw 'protoc-gen-go-grpc installation failed' }
} finally { $env:GOBIN = $savedGoBin }
$env:Path = 'D:\tools\meshops-bin;D:\tools\protoc-36.0-rc1\bin;' + $env:Path
protoc --version
buf --version
protoc-gen-go --version
protoc-gen-go-grpc --version
./scripts/verify-proto.ps1
```

最后一条命令在工程根目录执行。四个版本输出应分别为 `libprotoc 36.0-rc1`、`1.72.0`、`protoc-gen-go v1.28.1`、`protoc-gen-go-grpc 1.2.0`。验证脚本会先检查兼容性，再将临时生成结果与仓库逐字节比较；不会覆盖 `gen/`。
