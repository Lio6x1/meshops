# Maintainer acceptance: an empty learner directory receives each checkpoint in order.
# Runs local process checks. Business stage Compose uses a distinct project/volumes.
param([switch]$BusinessOnly, [string]$ResumeEvidence)
$ErrorActionPreference = 'Stop'
$chainMaterialRoot = $PSScriptRoot
$repoRoot = [IO.Path]::GetFullPath((Join-Path $chainMaterialRoot '../../..'))
$runID = [Guid]::NewGuid().ToString('N')
if ($ResumeEvidence) {
    $prior = Get-Content (Join-Path $ResumeEvidence 'result.json') -Raw | ConvertFrom-Json
    if ($prior.runID -notmatch '^[a-f0-9]{32}$' -or $prior.passed -or @($prior.completedStages).Count -ne 8 -or $prior.completedStages[-1].stage -ne 'z08') { throw 'Resume requires the interrupted eight-stage result ending at z08' }
    $expectedEvidence = [IO.Path]::GetFullPath((Join-Path $chainMaterialRoot "verification/chain-$($prior.runID)"))
    if ([IO.Path]::GetFullPath($ResumeEvidence) -ne $expectedEvidence) { throw 'Resume evidence must belong to this course' }
    $runID = $prior.runID
}
$learner = Join-Path $repoRoot ".cache/course-chain-$runID/learner"
$evidence = Join-Path $chainMaterialRoot "verification/chain-$runID"
New-Item -ItemType Directory -Force -Path $evidence | Out-Null
$results = [Collections.Generic.List[object]]::new()
$stages = @('z01','z02','z03','z04','z05','z06','z07','z08','z09')
if ($ResumeEvidence) {
    $record = Get-Content (Join-Path $learner '.course/checkpoint.json') -Raw | ConvertFrom-Json
    if ($record.stage -ne 'z08') { throw 'Resume learner must still be at z08' }
    foreach ($entry in $prior.completedStages) { $results.Add($entry) }
    $stages = @('z09')
}
$owned = [Collections.Generic.List[Diagnostics.Process]]::new()
$oldCompose = $env:COMPOSE_PROJECT_NAME
$copyProject = 'meshops-copy-' + $runID.Substring(0,8)
$restoreReference = $false
$stateStarted = $false
$copyStarted = $false
$failure = $null
function Run-Go([string[]]$Arguments, [string]$Log) {
    & go @Arguments *> (Join-Path $evidence $Log)
    if ($LASTEXITCODE -ne 0) { throw "go command failed; inspect $Log" }
}
function Stop-Owned {
    foreach ($process in $owned) {
        if (-not $process.HasExited) { $process.Kill(); if (-not $process.WaitForExit(10000)) { throw 'Owned child did not stop' } }
        $process.Dispose()
    }
    $owned.Clear()
}
function Start-Owned([string]$Exe, [string[]]$Arguments, [string]$Name) {
    $p = Start-Process -FilePath (Join-Path $learner $Exe) -WorkingDirectory $learner -ArgumentList $Arguments -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $evidence "$Name.out.txt") -RedirectStandardError (Join-Path $evidence "$Name.err.txt")
    $owned.Add($p)
    return $p
}
function Course([string[]]$Arguments) {
    $output = @(& (Join-Path $learner 'bin/course.exe') @Arguments 2> (Join-Path $evidence 'course-last-error.txt'))
    if ($LASTEXITCODE -ne 0) { throw "course $($Arguments[0]) failed" }
    return (($output -join "`n") | ConvertFrom-Json)
}
function Wait-Snapshot([string]$Endpoint, [string]$ID, [long]$Version) {
    $until = [DateTime]::UtcNow.AddSeconds(45)
    do {
        try {
            $snapshot = Course @('get','--endpoint',$Endpoint,'--id',$ID)
            if ($snapshot.found -and [long]$snapshot.version -ge $Version) { return $snapshot }
        } catch { }
        if (@($owned | Where-Object HasExited).Count) { throw 'Course child exited before snapshot' }
        Start-Sleep -Milliseconds 200
    } while ([DateTime]::UtcNow -lt $until)
    throw "Snapshot did not reach $ID version $Version"
}
function Wait-Course([string]$Endpoint) {
    $until = [DateTime]::UtcNow.AddSeconds(30)
    do {
        try { $null = Course @('get','--endpoint',$Endpoint,'--id','missing'); return } catch { }
        if (@($owned | Where-Object HasExited).Count) { throw 'Course child exited before ready' }
        Start-Sleep -Milliseconds 200
    } while ([DateTime]::UtcNow -lt $until)
    throw 'Course RPC not ready'
}
function Check-TrackedFiles {
    $record = Get-Content (Join-Path $learner '.course/checkpoint.json') -Raw | ConvertFrom-Json
    foreach ($file in $record.files) {
        if ((Get-FileHash -LiteralPath (Join-Path $learner $file.path)).Hash.ToLowerInvariant() -ne $file.sha256) { throw "Tracked file changed during verification: $($file.path)" }
    }
}
Push-Location $repoRoot
try {
    foreach ($stage in $stages) {
        & (Join-Path $chainMaterialRoot 'apply-checkpoint.ps1') -Stage $stage -Destination $learner
        Push-Location $learner
        try {
            $started = [DateTime]::UtcNow
            $verified = 'copy and build'
            Run-Go @('build','-p','4','./...') "$stage-build.txt"
            if (-not $BusinessOnly -and $stage -in @('z02','z03')) {
                & ./smoke.ps1 *> (Join-Path $evidence "$stage-smoke.txt")
                $verified = 'real RPC smoke'
            }
            if (-not $BusinessOnly -and $stage -in @('z04','z05','z06')) {
                . ./scripts/env.ps1
                Run-Go @('build','-o','bin/course.exe','./cmd/course') "$stage-course-build.txt"
                if ($stage -eq 'z04') {
                    $endpoint = '127.0.0.1:25151'
                    $null = Start-Owned 'bin/course.exe' @('serve','--role','all') "$stage-server"
                } else {
                    $env:COMPOSE_PROJECT_NAME = 'meshops-state-lessons'
                    if (-not $stateStarted) {
                        $stateStarted = $true
                        & docker compose up -d --wait --wait-timeout 180 *> (Join-Path $evidence 'state-compose.txt')
                        if ($LASTEXITCODE -ne 0) { throw 'State dependencies failed' }
                    }
                    $endpoint = if ($stage -eq 'z05') { '127.0.0.1:25252' } else { '127.0.0.1:25352' }
                    $null = Start-Owned 'bin/course.exe' @('serve','--role','ingest','-f','configs/server.yaml') "$stage-ingest"
                    $null = Start-Owned 'bin/course.exe' @('serve','--role','entity','-f','configs/entity.yaml') "$stage-entity"
                }
                Wait-Course $endpoint
                $ingestEndpoint = if ($stage -eq 'z04') { $endpoint } elseif ($stage -eq 'z05') { '127.0.0.1:25251' } else { '127.0.0.1:25351' }
                if ($stage -ne 'z06') {
                    # New manual versions avoid old isolated lesson state on reruns.
                    $version = [DateTimeOffset]::UtcNow.ToUnixTimeMilliseconds()
                    $null = Course @('report','--endpoint',$ingestEndpoint,'--source','personnel_sim','--id','person-001','--version',"$version")
                    $snapshot = Wait-Snapshot $endpoint 'person-001' $version
                    if ($snapshot.snapshot.power) { throw 'Personnel unexpectedly has power' }
                    $env:COURSE_INVALID_TOKEN = 'invalid-credential-with-at-least-32-bytes'
                    & ./bin/course.exe get --endpoint $endpoint --id person-001 --token-env COURSE_INVALID_TOKEN *> (Join-Path $evidence "$stage-unauthorized.txt")
                    if ($LASTEXITCODE -eq 0) { throw 'Wrong credential accepted' }
                    $snapshot | ConvertTo-Json -Depth 12 | Set-Content (Join-Path $evidence "$stage-snapshot.json")
                } else {
                    Run-Go @('build','-o','bin/gateway-simulator.exe','./cmd/gateway-simulator') 'z06-gateway-build.txt'
                    $null = Start-Owned 'bin/course.exe' @('watch','--endpoint',$endpoint,'--id','person-001') 'z06-watch'
                    foreach ($kind in @('person','drone','vehicle','robot','sensor','facility')) {
                        $source = if ($kind -eq 'person') { 'personnel_sim' } else { "$kind`_sim" }
                        & ./bin/gateway-simulator.exe --manifest configs/simulation.yaml --source $source --db ".local/$source.db" --offline --rate 10 --duration 1s *> (Join-Path $evidence "z06-$kind-offline.txt")
                        if ($LASTEXITCODE -ne 0) { throw 'Offline generation failed' }
                        & ./bin/gateway-simulator.exe --manifest configs/simulation.yaml --source $source --db ".local/$source.db" --endpoint $ingestEndpoint --drain-only --timeout 45s *> (Join-Path $evidence "z06-$kind-drain.txt")
                        if ($LASTEXITCODE -ne 0) { throw 'Queue drain failed' }
                        $snapshot = Wait-Snapshot $endpoint "$kind-001" 1
                        if ($snapshot.snapshot.entityType -ne $kind) { throw 'Wrong entity kind' }
                    }
                    if ((Get-Content (Join-Path $evidence 'z06-watch.out.txt') -Raw) -notmatch 'SNAPSHOT_END') { throw 'Subscription initialization boundary missing' }
                }
                Stop-Owned
                $verified = 'real RPC and identity; Kafka/Redis from z05; six durable gateways and subscription in z06'
            }
            if ($stage -eq 'z07' -or $ResumeEvidence) {
                if ($stateStarted) {
                    & docker compose -p meshops-state-lessons -f (Join-Path $chainMaterialRoot 'state-stages/z06/docker-compose.yml') stop *> (Join-Path $evidence 'state-stop.txt')
                    if ($LASTEXITCODE -ne 0) { throw 'State dependencies did not stop' }
                    $stateStarted = $false
                }
                # Save reference data and free its fixed local ports; never remove volumes.
                $restoreReference = $true
                & docker compose -p meshops-course -f (Join-Path $repoRoot 'docker-compose.yml') stop *> (Join-Path $evidence 'reference-stop.txt')
                if ($LASTEXITCODE -ne 0) { throw 'Reference dependencies did not stop' }
                $env:COMPOSE_PROJECT_NAME = $copyProject
                $copyStarted = $true
                if ($stage -eq 'z07') { & ./scripts/initialize.ps1 *> (Join-Path $evidence 'z07-initialize.txt') }
            }
            if ($stage -in @('z07','z08','z09')) {
                $env:COMPOSE_PROJECT_NAME = $copyProject
                if ($stage -ne 'z07') { & ./scripts/initialize.ps1 *> (Join-Path $evidence "$stage-initialize.txt") }
                try {
                    & ./scripts/start.ps1 -Simulators *> (Join-Path $evidence "$stage-start.txt")
                    & ./scripts/demo.ps1 *> (Join-Path $evidence "$stage-demo.txt")
                    $latest = Get-ChildItem results -Filter 'demo-*.json' | Sort-Object LastWriteTime -Descending | Select-Object -First 1
                    Copy-Item $latest.FullName (Join-Path $evidence "$stage-demo.json")
                    if ($stage -eq 'z07') {
                        & ./bin/opctl.exe history --entity person-001 *> (Join-Path $evidence 'z07-history-rejected.txt')
                        if ($LASTEXITCODE -ne 2) { throw 'Z07 unexpectedly offers state history' }
                    } else {
                        . ./scripts/env.ps1
                        $from = [DateTimeOffset]::UtcNow.AddMinutes(-10).ToString('O')
                        $to = [DateTimeOffset]::UtcNow.AddMinutes(1).ToString('O')
                        $history = @(& ./bin/opctl.exe history --entity person-001 --start $from --end $to)
                        if ($LASTEXITCODE -ne 0) { throw 'History RPC failed' }
                        $parsed = ($history -join "`n") | ConvertFrom-Json
                        if (@($parsed.samples).Count -lt 1) { throw 'No real history samples' }
                        $history | Set-Content (Join-Path $evidence "$stage-history.json")
                    }
                } finally { & ./scripts/stop.ps1 *> (Join-Path $evidence "$stage-stop.txt") }
                $verified = 'fresh isolated Compose; six snapshots; four tasks; history in z08/z09; same learner credentials and durable files'
            }
            Check-TrackedFiles
            $results.Add([ordered]@{stage=$stage;verified=$verified;seconds=([DateTime]::UtcNow-$started).TotalSeconds})
            "PASS $stage"
        } finally { Pop-Location }
    }
} catch {
    $failure = $_.Exception.Message
    throw
} finally {
    $cleanupErrors = [Collections.Generic.List[string]]::new()
    try { Stop-Owned } catch { $cleanupErrors.Add($_.Exception.Message) }
    if ($copyStarted) {
        & docker compose -p $copyProject -f (Join-Path $repoRoot 'docker-compose.yml') --profile verification stop *> (Join-Path $evidence 'copy-stop.txt')
        if ($LASTEXITCODE -ne 0) { $cleanupErrors.Add('Copy Compose cleanup failed') }
    }
    if ($restoreReference) {
        & docker compose -p meshops-course -f (Join-Path $repoRoot 'docker-compose.yml') --profile verification up -d --wait --wait-timeout 180 *> (Join-Path $evidence 'reference-restore.txt')
        if ($LASTEXITCODE -ne 0) { $cleanupErrors.Add('Reference dependencies restore failed') }
    }
    if ($stateStarted) {
        & docker compose -p meshops-state-lessons -f (Join-Path $chainMaterialRoot 'state-stages/z06/docker-compose.yml') stop *> (Join-Path $evidence 'state-final-stop.txt')
        if ($LASTEXITCODE -ne 0) { $cleanupErrors.Add('State Compose cleanup failed') }
    }
    $env:COMPOSE_PROJECT_NAME = $oldCompose
    [ordered]@{runID=$runID;learner=$learner;businessOnly=[bool]$BusinessOnly;completedStages=@($results.ToArray());failure=$failure;cleanupErrors=@($cleanupErrors.ToArray());passed=($results.Count -eq 9 -and -not $failure -and $cleanupErrors.Count -eq 0)} | ConvertTo-Json -Depth 5 | Set-Content (Join-Path $evidence 'result.json')
    Pop-Location
    "Evidence: $evidence"
    if ($cleanupErrors.Count) { throw "Cleanup incomplete: $($cleanupErrors -join '; '); original failure: $failure" }
}
