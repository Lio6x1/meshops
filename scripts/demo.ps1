param([int]$WaitSeconds = 45)
$ErrorActionPreference = 'Stop'
if ($WaitSeconds -lt 1 -or $WaitSeconds -gt 300) { throw 'WaitSeconds must be 1..300' }
. (Join-Path $PSScriptRoot 'env.ps1')
Push-Location $courseRoot
try {
    $opctl = Join-Path $courseRoot 'bin/opctl.exe'
    function Invoke-CourseCommand([string[]]$Arguments) {
        $lines = @(& $opctl @Arguments)
        if ($LASTEXITCODE -ne 0) { throw "opctl failed: $($Arguments[0]); inspect its JSON error on stderr" }
        return (($lines -join "`n") | ConvertFrom-Json)
    }
    $runId = [Guid]::NewGuid().ToString('D')
    $snapshots = @()
    foreach ($type in @('person','drone','vehicle','robot','sensor','facility')) {
        $snapshot = Invoke-CourseCommand @('snapshot','--entity',"$type-001")
        if (-not $snapshot.found -or $snapshot.snapshot.entityType -ne $type) { throw "Missing or mismatched snapshot: $type" }
        if ([DateTimeOffset]$snapshot.expiresAt -le [DateTimeOffset]::UtcNow) { throw "Snapshot expired: $type. Start simulators." }
        $snapshots += @{entityId=$snapshot.entityId;version=$snapshot.version;type=$type}
    }
    $completed = @()
    foreach ($type in @('person','drone','vehicle','robot')) {
        $arguments = @('task','create','--entity',"$type-001",'--key',"demo-$runId-$type",'--duration-seconds','1')
        $created = Invoke-CourseCommand $arguments
        $again = Invoke-CourseCommand $arguments
        if (-not $created.taskId -or $created.taskId -ne $again.taskId) { throw 'Idempotent create returned a different task ID' }
        $limit = [DateTime]::UtcNow.AddSeconds($WaitSeconds)
        do {
            $read = Invoke-CourseCommand @('task','get','--id',$created.taskId)
            $status = $read.task.status
            if ($status -eq 'TASK_STATUS_SUCCEEDED') { break }
            if ($status -in @('TASK_STATUS_FAILED','TASK_STATUS_CANCELLED','TASK_STATUS_TIMED_OUT','TASK_STATUS_REJECTED')) { throw "Task $($created.taskId) ended as $status" }
            Start-Sleep -Milliseconds 250
        } while ([DateTime]::UtcNow -lt $limit)
        if ($status -ne 'TASK_STATUS_SUCCEEDED') { throw "Task $($created.taskId) did not finish; last status: $status" }
        $effect = $read.task.resultJson | ConvertFrom-Json
        if ($effect.effect_count -ne 1 -or $effect.inspection_id -ne $created.taskId -or $effect.entity_id -ne "$type-001") { throw 'Task result is missing or has an inconsistent durable effect identity' }
        $history = Invoke-CourseCommand @('task','history','--id',$created.taskId)
        $dispatch = Invoke-CourseCommand @('dispatch','get','--task',$created.taskId)
        $completed += @{entityId="$type-001";taskId=$created.taskId;status=$status;result=$effect;dispatch=$dispatch;history=$history}
    }
    # A finite real subscription verifies the initial snapshot boundary and streaming path.
    $stream = @(& $opctl subscribe --entities person-001,drone-001,vehicle-001,robot-001,sensor-001,facility-001 --duration 3s)
    if ($LASTEXITCODE -ne 0) { throw 'subscription failed' }
    $frames = @($stream | ForEach-Object { $_ | ConvertFrom-Json })
    if (-not ($frames | Where-Object { $_.kind -eq 'ENTITY_UPDATE_KIND_SNAPSHOT_END' })) { throw 'subscription never completed its initial snapshot' }
    $dispatcher = Invoke-CourseCommand @('dispatcher','status','--token-env','MESHOPS_ADMIN_TOKEN')
    $result = @{runId=$runId;completedAt=[DateTime]::UtcNow.ToString('o');snapshots=$snapshots;tasks=$completed;subscriptionLines=$stream.Count;dispatcher=$dispatcher}
    New-Item -ItemType Directory -Force -Path 'results' | Out-Null
    $file = "results/demo-$runId.json"
    $result | ConvertTo-Json -Depth 32 | Set-Content -LiteralPath $file -Encoding utf8
    @{passed=$true;entities=$snapshots.Count;completedTasks=$completed.Count;evidence=$file} | ConvertTo-Json -Compress
} finally { Pop-Location }
