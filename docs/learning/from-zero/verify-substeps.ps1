param([switch]$SearchOnly,[switch]$WebOnly)
$ErrorActionPreference='Stop'
$materialRoot=$PSScriptRoot
$repoRoot=[IO.Path]::GetFullPath((Join-Path $materialRoot '../../..'))
$runRoot=Join-Path $repoRoot ('.cache/substeps-'+[guid]::NewGuid().ToString('N'))
$learner=Join-Path $runRoot 'learner'
[IO.Directory]::CreateDirectory($learner)|Out-Null
$stages=(Get-Content (Join-Path $materialRoot 'checkpoint-index.json') -Raw|ConvertFrom-Json).stages
$steps=Get-Content (Join-Path $materialRoot 'substep-index.json') -Raw|ConvertFrom-Json
$go='D:/go1.25.10/bin/go.exe'
$env:GOWORK='off'
$env:GOCACHE=Join-Path $repoRoot '.cache/go-build-reference'
$results=[Collections.Generic.List[object]]::new()
if($SearchOnly -and $WebOnly){throw 'Choose at most one focused verification mode'}
function Source-Files([string]$directory) {
    Get-ChildItem -LiteralPath $directory -File -Force
    foreach($child in Get-ChildItem -LiteralPath $directory -Directory -Force) {
        if($child.Name -notin @('node_modules','dist')){Source-Files $child.FullName}
    }
}
function Target([string]$relative) {
    if($relative -match '(^/|\\|:|(^|/)\.\.(/|$))'){throw "Unsafe path $relative"}
    $path=[IO.Path]::GetFullPath((Join-Path $learner $relative))
    if(-not $path.StartsWith($learner+[IO.Path]::DirectorySeparatorChar,[StringComparison]::OrdinalIgnoreCase)){throw 'Target outside fresh learner'}
    return $path
}
function Copy-Files($files,$removed) {
    foreach($path in $removed){$target=Target $path; if(Test-Path -LiteralPath $target){Remove-Item -LiteralPath $target}}
    foreach($file in $files){
        $source=Join-Path $materialRoot $file.source
        if((Get-FileHash -LiteralPath $source).Hash.ToLowerInvariant() -cne $file.sha256){throw "Source hash changed: $source"}
        $target=Target $file.path
        [IO.Directory]::CreateDirectory((Split-Path $target))|Out-Null
        Copy-Item -LiteralPath $source -Destination $target
    }
}
try {
    $stageIDs = @($stages.stage)
    if ($SearchOnly) {
        $base = $stages | Where-Object stage -eq 'z09'
        $files = foreach($file in $base.files){[pscustomobject]@{path=$file.path;source=$base.source+'/'+$file.path;sha256=$file.sha256}}
        Copy-Files $files @()
        $stageIDs = @('z10')
    } elseif ($WebOnly) {
        $base = $stages | Where-Object stage -eq 'z10'
        $files = foreach($file in $base.files){[pscustomobject]@{path=$file.path;source=$base.source+'/'+$file.path;sha256=$file.sha256}}
        Copy-Files $files @()
        $stageIDs = @('z11','z12','z13')
    }
    foreach($stageID in $stageIDs) {
        $stage=$stages|Where-Object stage -eq $stageID
        if($stageID -in $steps.stage) {
            foreach($step in @($steps|Where-Object stage -eq $stageID)) {
                Copy-Files $step.files $step.removed
                Push-Location $learner
                try {
                    & $go build ./... *> (Join-Path $runRoot ($step.id+'-build.txt'))
                    if($LASTEXITCODE -ne 0){throw "Build failed at $($step.id)"}
                    if($step.tests.Count) {
                        foreach($need in $step.needs) {
                            $envName = switch($need){'Redis'{'MESHOPS_TEST_REDIS_ADDR'} 'Kafka'{'MESHOPS_TEST_KAFKA_BROKERS'} 'MySQL'{'MESHOPS_TEST_MYSQL_ADMIN_DSN'}}
                            if(-not [Environment]::GetEnvironmentVariable($envName,'Process')){throw "Required real dependency is not configured: $envName"}
                        }
                        $log=Join-Path $runRoot ($step.id+'-test.jsonl')
                        & $go test @($step.packages) -run ('^('+($step.tests -join '|')+')$') -count=1 -json *> $log
                        if($LASTEXITCODE -ne 0){throw "Test failed at $($step.id)"}
                        $events=Get-Content $log|ForEach-Object {$_|ConvertFrom-Json}
                        foreach($name in $step.tests){if(-not @($events|Where-Object {$_.Action -eq 'pass' -and $_.Test -eq $name}).Count){throw "Test not passed or not found: $name"}}
                    }
                    if($step.id -eq 'z12-01') {
                        & node --experimental-strip-types --test web/tests/*.test.ts *> (Join-Path $runRoot ($step.id+'-browser.txt'))
                        if($LASTEXITCODE -ne 0){throw 'Browser domain tests failed'}
                    }
                    if($step.id -in @('z12-02','z13-01')) {
                        & ./scripts/frontend.ps1 -Action Install *> (Join-Path $runRoot ($step.id+'-npm.txt'))
                        & ./scripts/frontend.ps1 -Action Test *> (Join-Path $runRoot ($step.id+'-browser.txt'))
                        & ./scripts/frontend.ps1 -Action Build *> (Join-Path $runRoot ($step.id+'-frontend-build.txt'))
                    }
                } finally {Pop-Location}
                $results.Add([ordered]@{step=$step.id; build=$true; tests=@($step.tests); dependencies=@($step.needs); exactTestsPassed=($step.tests.Count -gt 0); browserTests=($step.id -in @('z12-01','z12-02','z13-01')); frontendBuild=($step.id -in @('z12-02','z13-01'))})
                Write-Output "$($step.id): build and required tests passed"
            }
        } else {
            $files=foreach($file in $stage.files){[pscustomobject]@{path=$file.path;source=$stage.source+'/'+$file.path;sha256=$file.sha256}}
            Copy-Files $files $stage.changes.removed
        }
        foreach($file in $stage.files){if((Get-FileHash -LiteralPath (Target $file.path)).Hash.ToLowerInvariant() -cne $file.sha256){throw "End-stage hash mismatch $stageID/$($file.path)"}}
        $actual=@(Source-Files $learner)
        if($actual.Count -ne $stage.files.Count){throw "Unexpected files remain at $stageID"}
    }
    $result=[ordered]@{passed=$true; learner=$learner; steps=$results; note='Build and exact named tests per substep, including configured real dependencies; no full fault/performance rerun.'}
} catch {$result=[ordered]@{passed=$false; learner=$learner; steps=$results; failure=$_.Exception.Message};throw}
finally {$result|ConvertTo-Json -Depth 8|Set-Content (Join-Path $runRoot 'result.json') -Encoding utf8; Write-Output "Evidence: $runRoot"}
