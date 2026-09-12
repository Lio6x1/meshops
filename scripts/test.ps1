param([switch]$Integration)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'env.ps1')
Push-Location $courseRoot
try {
    & go test ./... -count=1
    if ($LASTEXITCODE -ne 0) { throw 'unit tests failed' }
    & go vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'go vet failed' }
    if ($Integration) {
        & docker compose --profile verification up -d --wait redis-fault
        if ($LASTEXITCODE -ne 0) { throw 'dedicated Redis fault instance failed to start' }
        # initialize.ps1 must have completed; tasks tests create and clean only their own random databases.
        $env:MESHOPS_TEST_MYSQL_DSN = $env:MESHOPS_MYSQL_DSN
        $env:MESHOPS_TEST_MYSQL_ADMIN_DSN = $env:MESHOPS_MYSQL_DSN
        $env:MESHOPS_TEST_REDIS_ADDR = $env:MESHOPS_REDIS_ADDR
        $env:MESHOPS_TEST_KAFKA_BROKERS = $env:MESHOPS_KAFKA_BROKERS
        $env:MESHOPS_TEST_REDIS_FAULT_ADDR = '127.0.0.1:16380'
        $env:MESHOPS_TEST_KAFKA_DELETE_RECORDS = '1'
        & go test -tags integration ./... -v -count=1 -timeout 10m
        if ($LASTEXITCODE -ne 0) { throw 'integration tests failed' }
    }
} finally { Pop-Location }
