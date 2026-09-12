$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'search-env.ps1')
Push-Location $courseRoot
try {
    & docker compose -f docker-compose.yml -f compose.search.yml --profile search up -d --wait --wait-timeout 180 mysql kafka redis elasticsearch
    if ($LASTEXITCODE) { throw 'Search dependency startup failed' }
    & go build -o bin/search-admin.exe ./cmd/search-admin
    if ($LASTEXITCODE) { throw 'Search maintenance build failed' }
    if (-not (Test-Path -LiteralPath '.local/search-bootstrap.json')) {
        & ./bin/search-admin.exe
        if ($LASTEXITCODE) { throw 'Search bootstrap failed; keep incomplete marker for diagnosis' }
    } else {
        & ./bin/search-admin.exe --credentials-only
        if ($LASTEXITCODE) { throw 'Canal account provisioning failed' }
    }
    . ./scripts/search-env.ps1
    & docker compose -f docker-compose.yml -f compose.search.yml --profile search up -d canal
    if ($LASTEXITCODE) { throw 'Canal startup failed' }
    Write-Host 'Canal started. The Search worker must still be started and the end-to-end test must pass.'
} finally { Pop-Location }
