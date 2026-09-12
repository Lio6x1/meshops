param([switch]$Integration, [switch]$Race)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'env.ps1')
Push-Location $courseRoot
$testEnvNames = @('MESHOPS_TEST_MYSQL_DSN','MESHOPS_TEST_MYSQL_ADMIN_DSN','MESHOPS_TEST_REDIS_ADDR','MESHOPS_TEST_KAFKA_BROKERS','MESHOPS_TEST_REDIS_FAULT_ADDR','MESHOPS_TEST_KAFKA_DELETE_RECORDS','MESHOPS_TEST_ES_ENDPOINT')
$savedTestEnv = @{}
foreach ($name in $testEnvNames) { $savedTestEnv[$name] = [Environment]::GetEnvironmentVariable($name, 'Process') }
try {
    $raceArgs = @()
    if ($Race) { $raceArgs += '-race' }
    & go test @raceArgs ./... -count=1
    if ($LASTEXITCODE -ne 0) { throw 'unit tests failed' }
    & go vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'go vet failed' }
    if ($Integration) {
        Get-Command python -ErrorAction Stop | Out-Null
        & docker compose -f docker-compose.yml -f compose.search.yml --profile verification --profile search up -d --wait --wait-timeout 180 mysql kafka redis redis-fault elasticsearch
        if ($LASTEXITCODE -ne 0) { throw 'integration dependencies failed to start' }
        # 必须先完成 initialize.ps1；任务测试只创建和清理自己的随机数据库。
        $env:MESHOPS_TEST_MYSQL_DSN = $env:MESHOPS_MYSQL_DSN
        $env:MESHOPS_TEST_MYSQL_ADMIN_DSN = $env:MESHOPS_MYSQL_DSN
        $env:MESHOPS_TEST_REDIS_ADDR = $env:MESHOPS_REDIS_ADDR
        $env:MESHOPS_TEST_KAFKA_BROKERS = $env:MESHOPS_KAFKA_BROKERS
        $env:MESHOPS_TEST_REDIS_FAULT_ADDR = '127.0.0.1:16380'
        $env:MESHOPS_TEST_KAFKA_DELETE_RECORDS = '1'
        $env:MESHOPS_TEST_ES_ENDPOINT = 'http://127.0.0.1:19200'
        New-Item -ItemType Directory -Force -Path results | Out-Null
        $resultPath = Join-Path $courseRoot 'results/integration.jsonl'
        # 保留 JSON 结果，防止依赖未满足而跳过的测试被悄悄算作验收通过。
        & go test @raceArgs -tags integration ./... -json -count=1 -timeout 15m | Tee-Object -FilePath $resultPath
        if ($LASTEXITCODE -ne 0) { throw 'integration tests failed' }
        & python scripts/check-test-results.py $resultPath
        if ($LASTEXITCODE -ne 0) { throw 'required integration tests did not all execute' }
    }
} finally {
    foreach ($name in $testEnvNames) { [Environment]::SetEnvironmentVariable($name, $savedTestEnv[$name], 'Process') }
    Pop-Location
}
