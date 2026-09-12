# Fresh learner Z09 -> Z10 runtime acceptance. Dedicated Compose project/volumes;
# temporarily frees and restores only reference dependency ports, never deletes volumes.
param([switch]$FullIntegration)
$ErrorActionPreference = 'Stop'
$material = $PSScriptRoot
$repo = [IO.Path]::GetFullPath((Join-Path $material '../../..'))
$runID = [Guid]::NewGuid().ToString('N')
$evidence = Join-Path $repo ".cache/search-course-$runID"
$learner = Join-Path $evidence 'learner'
New-Item -ItemType Directory -Force -Path $evidence | Out-Null
$oldProject = $env:COMPOSE_PROJECT_NAME
$copyProject = 'meshops-search-copy-'+$runID.Substring(0,8)
$restore = @()
$copyStarted = $false
$failure = $null
$steps = [Collections.Generic.List[string]]::new()
$cleanup = [Collections.Generic.List[string]]::new()
function Check-Native([string]$Operation) { if ($LASTEXITCODE) { throw "$Operation failed; inspect $evidence" } }
function Check-FileHashes {
    $record = Get-Content (Join-Path $learner '.course/checkpoint.json') -Raw | ConvertFrom-Json
    foreach ($file in $record.files) {
        if ((Get-FileHash -LiteralPath (Join-Path $learner $file.path)).Hash.ToLowerInvariant() -cne $file.sha256) { throw "Learner source changed: $($file.path)" }
    }
}
Push-Location $repo
try {
    . ./scripts/processes.ps1
    if (Test-Path .local/processes.json) {
        foreach ($record in @(Get-Content .local/processes.json -Raw | ConvertFrom-Json)) {
            if (Get-CourseOwnedProcess $record) { throw 'Stop reference course applications before isolated course verification.' }
        }
    }
    # Keep the snapshot exact: restore only services that were already running.
    $restore = @(& docker compose -p meshops-course -f docker-compose.yml -f compose.search.yml --profile search ps --services --status running)
    Check-Native 'Reference inventory'
    if ($restore.Count) {
        & docker compose -p meshops-course -f docker-compose.yml -f compose.search.yml --profile search stop @restore *> (Join-Path $evidence 'reference-stop.txt')
        Check-Native 'Reference stop'
    }
    foreach ($stage in @('z01','z02','z03','z04','z05','z06','z07','z08','z09')) {
        & (Join-Path $material 'apply-checkpoint.ps1') -Stage $stage -Destination $learner *> (Join-Path $evidence "$stage-copy.txt")
    }
    $env:COMPOSE_PROJECT_NAME = $copyProject
    Push-Location $learner
    try {
        $copyStarted = $true
        & ./scripts/initialize.ps1 *> (Join-Path $evidence 'z09-initialize.txt')
        & ./scripts/start.ps1 -Simulators *> (Join-Path $evidence 'z09-start.txt')
        & ./scripts/demo.ps1 *> (Join-Path $evidence 'z09-demo.txt')
        . ./scripts/env.ps1
        $before = & ./bin/opctl.exe task list --page-size 100 | ConvertFrom-Json
        Check-Native 'Z09 task list'
        if (@($before.tasks).Count -lt 4) { throw 'Z09 did not create the four expected example tasks.' }
        & ./scripts/stop.ps1
        $credentialHash = (Get-FileHash .local/secrets.json).Hash
        $steps.Add('Z09 fresh initialize, six entities and four task executions')
        & (Join-Path $material 'apply-checkpoint.ps1') -Stage z10 -Destination $learner *> (Join-Path $evidence 'z10-copy.txt')
        if ((Get-FileHash .local/secrets.json).Hash -cne $credentialHash) { throw 'Upgrade changed learner credentials.' }
        & ./scripts/build.ps1 *> (Join-Path $evidence 'z10-build.txt')
        & ./scripts/initialize-search.ps1 *> (Join-Path $evidence 'z10-initialize.txt')
        & ./scripts/start.ps1 -Search -Simulators *> (Join-Path $evidence 'z10-start.txt')
        & ./scripts/demo-search.ps1 *> (Join-Path $evidence 'z10-demo.txt')
        . ./scripts/search-env.ps1
        $found = & ./bin/opctl.exe task search --page-size 100 | ConvertFrom-Json
        Check-Native 'Z10 imported task search'
        foreach ($task in $before.tasks) {
            if ($task.taskId -notin @($found.tasks.taskId)) { throw 'A pre-upgrade task is missing from search.' }
        }
        $steps.Add('Z10 preserves credentials, imports pre-upgrade tasks and synchronizes new terminal task')
        & ./scripts/stop.ps1
        & ./scripts/test-search-rebuild.ps1 *> (Join-Path $evidence 'z10-rebuild.txt')
        & ./scripts/start.ps1 -Search -Simulators *> (Join-Path $evidence 'z10-restart.txt')
        & ./scripts/demo-search.ps1 *> (Join-Path $evidence 'z10-after-rebuild.txt')
        $steps.Add('Missing index and incomplete bootstrap rebuilt; business checksums preserved; new CDC converges')
        if ($FullIntegration) {
            # Exercise the published learner-facing entry point, including ES
            # configuration and the mandatory-test gate, in this isolated stack.
            & ./scripts/stop.ps1
            & ./scripts/test.ps1 -Integration *> (Join-Path $evidence 'z10-full-integration.txt')
            $steps.Add('Published test.ps1 -Integration passed the required-test gate')
        }
        Check-FileHashes
        $steps.Add('Published learner source hashes unchanged')
    } finally {
        try { & ./scripts/stop.ps1 *> (Join-Path $evidence 'learner-stop.txt') } catch { $cleanup.Add($_.Exception.Message) }
        Pop-Location
    }
} catch { $failure=$_.Exception.Message; throw }
finally {
    if ($copyStarted) {
        $copyFiles = @('-f',(Join-Path $learner 'docker-compose.yml'))
        if (Test-Path -LiteralPath (Join-Path $learner 'compose.search.yml')) { $copyFiles += @('-f',(Join-Path $learner 'compose.search.yml')) }
        & docker compose -p $copyProject @copyFiles --profile search --profile verification stop *> (Join-Path $evidence 'copy-stop.txt')
        if ($LASTEXITCODE) { $cleanup.Add('Learner dependencies did not stop') }
    }
    $env:COMPOSE_PROJECT_NAME = $oldProject
    if ($restore.Count) {
        try {
            # Reference Canal may require its own secrets/start position. Loading
            # them here never prints or copies them into the learner directory.
            . (Join-Path $repo 'scripts/search-env.ps1') -AllowIncomplete
            & docker compose -p meshops-course -f (Join-Path $repo 'docker-compose.yml') -f (Join-Path $repo 'compose.search.yml') --profile search up -d --wait --wait-timeout 180 @restore *> (Join-Path $evidence 'reference-restore.txt')
            if ($LASTEXITCODE) { $cleanup.Add('Reference dependency restore failed') }
        } catch { $cleanup.Add($_.Exception.Message) }
    }
    $requiredSteps = if ($FullIntegration) { 5 } else { 4 }
    @{passed=(-not $failure -and -not $cleanup.Count -and $steps.Count -eq $requiredSteps);learner=$learner;project=$copyProject;steps=@($steps.ToArray());failure=$failure;cleanupErrors=@($cleanup.ToArray())} | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath (Join-Path $evidence 'result.json') -Encoding utf8
    Pop-Location
    Write-Host "Evidence: $evidence"
    if ($cleanup.Count) { throw ('Cleanup incomplete: '+($cleanup -join '; ')) }
}
