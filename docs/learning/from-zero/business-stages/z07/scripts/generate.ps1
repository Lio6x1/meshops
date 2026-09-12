$ErrorActionPreference = 'Stop'
$courseRoot = Split-Path $PSScriptRoot -Parent
Push-Location $courseRoot
try {
    Get-Command protoc,protoc-gen-go,protoc-gen-go-grpc -ErrorAction Stop | Out-Null
    New-Item -ItemType Directory -Force -Path gen | Out-Null
    $includePath = (Resolve-Path proto).Path
    $protoFiles = Get-ChildItem proto -Recurse -Filter '*.proto' | ForEach-Object { $_.FullName }
    & protoc -I $includePath --go_out=gen --go_opt=paths=source_relative --go-grpc_out=gen --go-grpc_opt=paths=source_relative $protoFiles
    if ($LASTEXITCODE -ne 0) { throw 'protoc failed' }
} finally { Pop-Location }
