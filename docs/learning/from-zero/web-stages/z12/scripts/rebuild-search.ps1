# 维护窗口只重建任务搜索投影；Task/Outbox 和其他学习进程保持可用。
# 此时不得并发运行其他启动操作。
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'search-env.ps1') -AllowIncomplete
. (Join-Path $PSScriptRoot 'processes.ps1')
$lock = [IO.File]::Open((Join-Path $courseRoot '.local/search-maintenance.lock'), [IO.FileMode]::OpenOrCreate, [IO.FileAccess]::ReadWrite, [IO.FileShare]::None)
Push-Location $courseRoot
try {
    function Invoke-RebuildCompose([string[]]$Arguments) {
        & docker compose -f docker-compose.yml -f compose.search.yml --profile search @Arguments
        if ($LASTEXITCODE) { throw ('Search maintenance Compose failed: '+($Arguments -join ' ')) }
    }
    # 停止任何服务前先构建并做预检；构建目标程序不能仍在运行。
    & go build -o bin/search-admin.exe ./cmd/search-admin
    if ($LASTEXITCODE) { throw 'Search maintenance build failed.' }
    $recordPath = Join-Path $courseRoot '.local/processes.json'
    $records = @()
    if (Test-Path -LiteralPath $recordPath) { $records = @(Get-Content -LiteralPath $recordPath -Raw | ConvertFrom-Json) }
    $restart = $false
    foreach ($record in @($records | Where-Object name -eq 'search')) {
        if (Get-CourseOwnedProcess $record) { $restart = $true; Stop-CourseOwnedProcess $record }
    }
    $records = @($records | Where-Object name -ne 'search')
    ConvertTo-Json -InputObject @($records) | Set-Content -LiteralPath $recordPath -Encoding utf8
    Assert-CoursePortsAvailable @(Get-CourseServicePorts $courseRoot @('search'))
    & ./bin/search-admin.exe --invalidate-only
    if ($LASTEXITCODE) { throw 'Could not invalidate search readiness; no derived data reset.' }
    Invoke-RebuildCompose @('stop','canal')
    # 同时移除已停止的容器，因为它的可写层包含 TSDB 文件。
    Invoke-RebuildCompose @('rm','-f','canal')
    # 只操作专用 Canal 元数据卷内的固定 Linux 路径；不得触碰
    # MySQL、Redis、Kafka 数据卷或 Windows 源码目录。
    Invoke-RebuildCompose @('run','--rm','--no-deps','--entrypoint','/bin/sh','canal','-c','set -eu; test -d /home/admin/canal-server/meta; rm -rf -- /home/admin/canal-server/meta/meshops')
    Invoke-RebuildCompose @('up','-d','--wait','--wait-timeout','180','mysql','kafka','redis','elasticsearch')
    & ./bin/search-admin.exe --rebuild
    if ($LASTEXITCODE) { throw 'Search rebuild incomplete; Search stays stopped. Correct the error and rerun this script.' }
    . ./scripts/search-env.ps1
    Invoke-RebuildCompose @('up','-d','canal')
    if ($restart) { & ./scripts/start-search.ps1 }
    Write-Host 'Snapshot rebuilt and Canal restarted. Run start-search.ps1 if Search was stopped, then demo-search.ps1 to verify new changes.'
} finally {
    # 失败时有意保持 Search 停止，避免基于不完整快照提供服务。
    Pop-Location
    $lock.Dispose()
}
