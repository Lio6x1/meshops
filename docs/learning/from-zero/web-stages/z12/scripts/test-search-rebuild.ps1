# 显式恢复演练，要求先停止学习应用。只删除派生任务索引，
# 并将其标记置为未完成，然后执行重建。
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'search-env.ps1') -AllowIncomplete
. (Join-Path $PSScriptRoot 'processes.ps1')
$recordPath = Join-Path $courseRoot '.local/processes.json'
if (Test-Path -LiteralPath $recordPath) {
    foreach ($record in @(Get-Content -LiteralPath $recordPath -Raw | ConvertFrom-Json)) {
        if (Get-CourseOwnedProcess $record) { throw 'Stop course applications with scripts/stop.ps1 before this integrity drill.' }
    }
}
Assert-CoursePortsAvailable @(Get-CourseServicePorts $courseRoot @('entity','task','dispatcher','ingest','search'))
Push-Location $courseRoot
try {
    function Get-BusinessChecksums {
        # 只读计算全部业务表的校验和，包含 Outbox 和执行回报。
        # 口令始终留在 MySQL 容器内部。
        $rows = & docker compose exec -T mysql sh -c 'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql -uroot -N -B meshops_course -e "CHECKSUM TABLE integration_sources,entities,entity_history_samples,tasks,task_status_history,outbox_events,consumer_dedup,task_dispatches,task_execution_reports,history_sample_keys,course_bindings EXTENDED;"'
        if ($LASTEXITCODE) { throw 'Could not read business checksums.' }
        if (@($rows).Count -ne 11 -or ($rows -join "`n") -match '\bNULL\b') { throw 'Incomplete business checksum response.' }
        return ($rows -join "`n")
    }
    $before = Get-BusinessChecksums
    & go build -o bin/search-admin.exe ./cmd/search-admin
    if ($LASTEXITCODE) { throw 'Maintenance build failed.' }
    & ./bin/search-admin.exe --invalidate-only
    if ($LASTEXITCODE) { throw 'Could not simulate incomplete bootstrap.' }
    $blocked = $false
    try { . ./scripts/search-env.ps1 } catch { $blocked = $true }
    if (-not $blocked) { throw 'Incomplete bootstrap was accepted by normal startup.' }
    try { Invoke-RestMethod -Method Delete -Uri 'http://127.0.0.1:19200/meshops-tasks-v1' -TimeoutSec 10 | Out-Null }
    catch { if ([int]$_.Exception.Response.StatusCode -ne 404) { throw } }
    & ./scripts/rebuild-search.ps1
    $after = Get-BusinessChecksums
    if ($before -cne $after) { throw 'Business table checksums changed during search-only rebuild.' }
    $taskCount = & docker compose exec -T mysql sh -c 'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql -uroot -N -B meshops_course -e "SELECT COUNT(*) FROM tasks;"'
    if ($LASTEXITCODE -or $taskCount -notmatch '^\d+$') { throw 'Could not read source task count.' }
    Invoke-RestMethod -Method Post -Uri 'http://127.0.0.1:19200/meshops-tasks-v1/_refresh' -TimeoutSec 10 | Out-Null
    $indexed = Invoke-RestMethod -Uri 'http://127.0.0.1:19200/meshops-tasks-v1/_count' -TimeoutSec 10
    if ([long]$indexed.count -ne [long]$taskCount) { throw 'Rebuilt index document count does not match source tasks.' }
    . ./scripts/search-env.ps1
    $marker = Get-Content .local/search-bootstrap.json -Raw | ConvertFrom-Json
    if (-not $marker.complete -or -not $marker.index_uuid -or $marker.cdc_start -lt 0) { throw 'Rebuild did not publish a valid completed marker.' }
    New-Item -ItemType Directory -Force results | Out-Null
    $path = 'results/search-rebuild-'+[Guid]::NewGuid().ToString('N')+'.json'
    @{ passed=$true; incompleteStartupRefused=$blocked; missingIndexRebuilt=$true; businessChecksumsUnchanged=$true; taskCount=[long]$taskCount; indexedCount=[long]$indexed.count; checksums=$after; bootstrap=$marker; checkedAt=[DateTime]::UtcNow.ToString('o') } | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath $path -Encoding utf8
    Write-Host ('PASS: incomplete marker + missing ES index recovery; all 11 business table checksums unchanged. Evidence: '+$path)
} finally { Pop-Location }
