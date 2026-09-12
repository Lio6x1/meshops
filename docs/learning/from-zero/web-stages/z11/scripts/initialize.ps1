$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'env.ps1')
Push-Location $courseRoot
try {
    & docker compose up -d --wait --wait-timeout 180
    if ($LASTEXITCODE -ne 0) { throw 'dependency startup failed; inspect docker compose logs' }
    & (Join-Path $PSScriptRoot 'build.ps1')
    & ./bin/opctl.exe seed --manifest configs/simulation.yaml --token-env MESHOPS_ADMIN_TOKEN
    if ($LASTEXITCODE -ne 0) { throw 'schema, binding seed or Kafka initialization failed' }
} finally { Pop-Location }
