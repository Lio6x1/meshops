$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'search-env.ps1')
Push-Location $courseRoot
try {
    $runId = [Guid]::NewGuid().ToString('N')
    $keyword = 'searchproof' + $runId
    function Invoke-SearchDemoOpctl([string[]]$Arguments) {
        $raw = & ./bin/opctl.exe @Arguments
        if ($LASTEXITCODE) { throw ('opctl failed: ' + ($Arguments -join ' ')) }
        return ($raw | ConvertFrom-Json)
    }
    # RPC 就绪不代表重启的网关已补传完旧遥测并生成新鲜快照。
    # 应等待快照更新，不能放宽 Task 的新鲜度校验。
    $entityLimit = [DateTime]::UtcNow.AddSeconds(45)
    $entityReady = $false
    do {
        $snapshot = Invoke-SearchDemoOpctl @('snapshot','--entity','drone-001')
        if ($snapshot.found -and $snapshot.expiresAt -and
            [DateTimeOffset]$snapshot.expiresAt -gt [DateTimeOffset]::UtcNow.AddSeconds(5) -and
            $snapshot.snapshot.status -notin @('offline','busy','fault') -and
            'inspect' -in @($snapshot.snapshot.capability.supportedTasks)) {
            $entityReady = $true
            break
        }
        Start-Sleep -Milliseconds 300
    } while ([DateTime]::UtcNow -lt $entityLimit)
    if (-not $entityReady) { throw 'Drone did not publish a fresh inspect-capable snapshot; inspect drone_sim and Entity logs.' }
    $created = Invoke-SearchDemoOpctl @('task','create','--entity','drone-001','--key',$runId,'--duration-seconds','1','--note',$keyword)
    $limit = [DateTime]::UtcNow.AddSeconds(45)
    $matched = $false
    do {
        $fact = Invoke-SearchDemoOpctl @('task','get','--id',$created.taskId)
        $found = Invoke-SearchDemoOpctl @('task','search','--keyword',$keyword,'--entity','drone-001','--page-size','10')
        $hits = @($found.tasks | Where-Object taskId -eq $created.taskId)
        if ($fact.task.status -eq 'TASK_STATUS_SUCCEEDED' -and $hits.Count -eq 1 -and $hits[0].status -eq $fact.task.status -and $hits[0].statusVersion -eq $fact.task.statusVersion) { $matched=$true; break }
        Start-Sleep -Milliseconds 300
    } while ([DateTime]::UtcNow -lt $limit)
    if (-not $matched) { throw 'Search did not converge to the real task terminal status within 45 seconds. Inspect Canal and Search logs.' }
    New-Item -ItemType Directory -Force -Path results | Out-Null
    $resultPath = 'results/search-demo-' + $runId + '.json'
    @{ passed=$true; taskId=$created.taskId; keyword=$keyword; fact=$fact.task; search=$hits[0]; checkedAt=[DateTime]::UtcNow.ToString('o') } | ConvertTo-Json -Depth 20 | Set-Content -LiteralPath $resultPath -Encoding utf8
    Write-Host ('PASS: real task -> MySQL -> Canal -> Kafka -> Search/ES. Evidence: '+$resultPath)
} finally { Pop-Location }
