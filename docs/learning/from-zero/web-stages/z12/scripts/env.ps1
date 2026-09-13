# 每个新终端都要点加载此文件：. ./scripts/env.ps1
$ErrorActionPreference = 'Stop'
$courseRoot = Split-Path $PSScriptRoot -Parent
$localPath = Join-Path $courseRoot '.local'
New-Item -ItemType Directory -Force -Path $localPath | Out-Null
$secretPath = Join-Path $localPath 'secrets.json'
if (-not (Test-Path -LiteralPath $secretPath)) {
    $names = @('MESHOPS_CURSOR_KEY','MESHOPS_OPERATOR_TOKEN','MESHOPS_ADMIN_TOKEN','MESHOPS_TASK_TOKEN','MESHOPS_DISPATCHER_TOKEN')
    foreach ($type in @('PERSON','DRONE','VEHICLE','ROBOT','SENSOR','FACILITY')) { $names += "MESHOPS_${type}_SOURCE_TOKEN" }
    foreach ($type in @('PERSON','DRONE','VEHICLE','ROBOT')) { $names += "MESHOPS_${type}_EXECUTOR_TOKEN" }
    $values = @{}
    foreach ($name in $names) {
        $bytes = New-Object byte[] 32
        $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
        try { $rng.GetBytes($bytes) } finally { $rng.Dispose() }
        $values[$name] = [Convert]::ToBase64String($bytes)
    }
    $values | ConvertTo-Json | Set-Content -LiteralPath $secretPath -Encoding utf8
}
$secrets = Get-Content -LiteralPath $secretPath -Raw | ConvertFrom-Json
foreach ($property in $secrets.PSObject.Properties) { [Environment]::SetEnvironmentVariable($property.Name, [string]$property.Value, 'Process') }
# 这些是专用本地教学容器的凭证，不能作为生产账户使用。
$env:MESHOPS_MYSQL_DSN = 'root:course_local_root@tcp(127.0.0.1:13306)/meshops_course?parseTime=true&loc=UTC'
$env:MESHOPS_KAFKA_BROKERS = 'localhost:19092'
$env:MESHOPS_REDIS_ADDR = 'localhost:16379'
$env:MESHOPS_INGEST_ENDPOINT = '127.0.0.1:50051'
$env:MESHOPS_ENTITY_ENDPOINT = '127.0.0.1:50052'
$env:MESHOPS_TASK_ENDPOINT = '127.0.0.1:50053'
$env:MESHOPS_DISPATCHER_ENDPOINT = '127.0.0.1:50054'

# 成品启用个人会话身份；机器来源和执行方继续使用原凭证。
$env:MESHOPS_ACCOUNT_AUTH = '1'
