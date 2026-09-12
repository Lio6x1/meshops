# The browser API is an explicit allowlist separate from the gRPC wire contract.
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
Push-Location $root
try {
    $version = & protoc-gen-grpc-gateway --version
    if ($LASTEXITCODE -ne 0 -or $version -notmatch 'Version v2\.27\.7([, ]|$)') { throw 'Install protoc-gen-grpc-gateway@v2.27.7; see the fullstack guide.' }
    & protoc -I proto --grpc-gateway_out=gen --grpc-gateway_opt=paths=source_relative --grpc-gateway_opt=grpc_api_configuration=proto/http.yaml proto/entity/v1/entity.proto proto/task/v1/task.proto proto/dispatcher/v1/dispatcher.proto proto/search/v1/search.proto
    if ($LASTEXITCODE -ne 0) { throw 'HTTP gateway generation failed' }
} finally { Pop-Location }
