# 要求 Task/Search 和模拟器已启动。仅停止此 Compose 项目的
# Canal/ES 容器，保留任务源数据和全部命名卷。
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'search-env.ps1')
Push-Location $courseRoot
$recover = [Collections.Generic.List[string]]::new()
try {
    function Invoke-RecoveryCompose([string[]]$Arguments) {
        & docker compose -f docker-compose.yml -f compose.search.yml --profile search @Arguments
        if ($LASTEXITCODE) { throw ('Compose failed: '+($Arguments -join ' ')) }
    }
    function Invoke-RecoveryOpctl([string[]]$Arguments) {
        $raw=& ./bin/opctl.exe @Arguments
        if ($LASTEXITCODE) { throw ('opctl failed: '+($Arguments -join ' ')) }
        return ($raw | ConvertFrom-Json)
    }
    $evidence=@()
    foreach ($dependency in @('canal','elasticsearch')) {
        $recover.Add($dependency)
        Invoke-RecoveryCompose @('stop',$dependency)
        $id=[Guid]::NewGuid().ToString('N')
        $keyword='recoveryproof'+$id
        $created=Invoke-RecoveryOpctl @('task','create','--entity','drone-001','--key',$id,'--duration-seconds','1','--note',$keyword)
        $limit=[DateTime]::UtcNow.AddSeconds(30)
        do {
            $fact=Invoke-RecoveryOpctl @('task','get','--id',$created.taskId)
            if ($fact.task.status -eq 'TASK_STATUS_SUCCEEDED') { break }
            Start-Sleep -Milliseconds 250
        } while ([DateTime]::UtcNow -lt $limit)
        if ($fact.task.status -ne 'TASK_STATUS_SUCCEEDED') { throw 'Task flow did not complete while search dependency was stopped' }
        if ($dependency -eq 'canal') {
            $before=Invoke-RecoveryOpctl @('task','search','--keyword',$keyword)
            if (@($before.tasks | Where-Object taskId -eq $created.taskId).Count) { throw 'Stopped Canal unexpectedly delivered a new task' }
        } else {
            $diagnostic=Join-Path $courseRoot '.local/search-unavailable-test.json'
            $discard=& ./bin/opctl.exe task search --keyword $keyword 2> $diagnostic
            if ($LASTEXITCODE -eq 0 -or (Get-Content $diagnostic -Raw) -notmatch 'Unavailable') { throw 'Unavailable ES must fail search explicitly' }
        }
        Invoke-RecoveryCompose @('up','-d','--wait','--wait-timeout','120',$dependency)
        $limit=[DateTime]::UtcNow.AddSeconds(45)
        $matched=$false
        do {
            $after=Invoke-RecoveryOpctl @('task','search','--keyword',$keyword)
            $hits=@($after.tasks | Where-Object taskId -eq $created.taskId)
            if ($hits.Count -eq 1 -and $hits[0].status -eq $fact.task.status -and $hits[0].statusVersion -eq $fact.task.statusVersion) { $matched=$true;break }
            Start-Sleep -Milliseconds 300
        } while ([DateTime]::UtcNow -lt $limit)
        if (-not $matched) { throw ('Search did not recover after '+$dependency) }
        $recover.Remove($dependency) | Out-Null
        $evidence+=@{dependency=$dependency;taskId=$created.taskId;taskContinued=$true;searchRecovered=$true;statusVersion=$hits[0].statusVersion}
    }
    $resultPath='results/search-recovery-'+[Guid]::NewGuid().ToString('N')+'.json'
    @{passed=$true;scenarios=$evidence;checkedAt=[DateTime]::UtcNow.ToString('o')} | ConvertTo-Json -Depth 10 | Set-Content -LiteralPath $resultPath -Encoding utf8
    Write-Host ('PASS: Canal and ES stop/recovery. Evidence: '+$resultPath)
} finally {
    foreach ($dependency in $recover) {
        & docker compose -f docker-compose.yml -f compose.search.yml --profile search up -d $dependency
        if ($LASTEXITCODE) { Write-Warning ('Could not restore '+$dependency) }
    }
    Pop-Location
}
