# Maintenance window: rebuild only the task search projection. Task/Outbox and
# all other course processes remain available. Run no other startup concurrently.
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
    # Build/preflight before stopping anything. The executable must not be live.
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
    # Remove its stopped container too: its writable layer contains TSDB files.
    Invoke-RebuildCompose @('rm','-f','canal')
    # Fixed Linux path inside the dedicated Canal metadata volume; never touch
    # MySQL, Redis, Kafka volumes or the Windows source directory.
    Invoke-RebuildCompose @('run','--rm','--no-deps','--entrypoint','/bin/sh','canal','-c','set -eu; test -d /home/admin/canal-server/meta; rm -rf -- /home/admin/canal-server/meta/meshops')
    Invoke-RebuildCompose @('up','-d','--wait','--wait-timeout','180','mysql','kafka','redis','elasticsearch')
    & ./bin/search-admin.exe --rebuild
    if ($LASTEXITCODE) { throw 'Search rebuild incomplete; Search stays stopped. Correct the error and rerun this script.' }
    . ./scripts/search-env.ps1
    Invoke-RebuildCompose @('up','-d','canal')
    if ($restart) { & ./scripts/start-search.ps1 }
    Write-Host 'Snapshot rebuilt and Canal restarted. Run start-search.ps1 if Search was stopped, then demo-search.ps1 to verify new changes.'
} finally {
    # Failures deliberately do not restart Search against a partial snapshot.
    Pop-Location
    $lock.Dispose()
}
