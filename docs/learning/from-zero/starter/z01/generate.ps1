$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot

New-Item -ItemType Directory -Force gen | Out-Null
protoc --proto_path=proto --go_out=. --go_opt=module=example.com/meshops-course --go-grpc_out=. --go-grpc_opt=module=example.com/meshops-course proto/common/v1/entity.proto proto/entity/v1/entity.proto
if ($LASTEXITCODE -ne 0) { throw 'protoc failed' }
