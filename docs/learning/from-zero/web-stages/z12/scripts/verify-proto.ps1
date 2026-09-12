$ErrorActionPreference = 'Stop'
$courseRoot = Split-Path $PSScriptRoot -Parent
Push-Location $courseRoot
try {
    Get-Command buf,protoc,protoc-gen-go,protoc-gen-go-grpc -ErrorAction Stop | Out-Null
    if ((& protoc --version) -notmatch '36\.0-rc1$') { throw 'protoc must be 36.0-rc1 for byte-identical generation' }
    if ((& protoc-gen-go --version) -notmatch 'v1\.28\.1$') { throw 'protoc-gen-go must be v1.28.1' }
    if ((& protoc-gen-go-grpc --version) -notmatch '1\.2\.0$') { throw 'protoc-gen-go-grpc must be 1.2.0' }
    & buf lint proto
    if ($LASTEXITCODE -ne 0) { throw 'proto lint failed' }
    & buf breaking proto --against testdata/proto/reference-v1.bin
    if ($LASTEXITCODE -ne 0) { throw 'reference protocol breaking change' }
    & buf breaking proto --against testdata/proto/legacy.bin --config '{"version":"v1","breaking":{"use":["WIRE"]}}'
    if ($LASTEXITCODE -ne 0) { throw 'legacy wire compatibility failed' }
    $output = Join-Path $courseRoot ('.local/proto-check-' + [Guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Path $output -Force | Out-Null
    $files = @(Get-ChildItem proto -Recurse -Filter '*.proto' | ForEach-Object { $_.FullName })
    $includePath = (Resolve-Path proto).Path
    & protoc -I $includePath "--go_out=$output" --go_opt=paths=source_relative "--go-grpc_out=$output" --go-grpc_opt=paths=source_relative $files
    if ($LASTEXITCODE -ne 0) { throw 'generation failed' }
    if (Test-Path -LiteralPath 'proto/http.yaml') {
        Get-Command protoc-gen-grpc-gateway -ErrorAction Stop | Out-Null
        if ((& protoc-gen-grpc-gateway --version) -notmatch 'Version v2\.27\.7([, ]|$)') { throw 'protoc-gen-grpc-gateway must be v2.27.7' }
        & protoc -I proto "--grpc-gateway_out=$output" --grpc-gateway_opt=paths=source_relative --grpc-gateway_opt=grpc_api_configuration=proto/http.yaml proto/entity/v1/entity.proto proto/task/v1/task.proto proto/dispatcher/v1/dispatcher.proto proto/search/v1/search.proto
        if ($LASTEXITCODE -ne 0) { throw 'HTTP gateway generation failed' }
    }
    $generated = @(Get-ChildItem $output -Recurse -Filter '*.go')
    $existing = @(Get-ChildItem gen -Recurse -Filter '*.go')
    if ($generated.Count -ne $existing.Count) { throw 'generated file count differs' }
    foreach ($file in $generated) {
        $relative = $file.FullName.Substring($output.Length).TrimStart('\','/')
        $target = Join-Path (Join-Path $courseRoot 'gen') $relative
        if (-not (Test-Path -LiteralPath $target)) { throw "missing generated file: $relative" }
        if ((Get-FileHash -LiteralPath $file.FullName).Hash -ne (Get-FileHash -LiteralPath $target).Hash) { throw "generated content differs: $relative" }
    }
    'Proto lint, compatibility and exact generation passed.'
} finally { Pop-Location }
