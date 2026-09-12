param([switch]$AllStages)
$ErrorActionPreference = 'Stop'
$courseRoot = $PSScriptRoot
$scriptPath = Join-Path $courseRoot 'apply-checkpoint.ps1'
$tokens = $null
$parseErrors = $null
[Management.Automation.Language.Parser]::ParseFile($scriptPath, [ref]$tokens, [ref]$parseErrors) | Out-Null
if ($parseErrors.Count) { throw "Checkpoint script does not parse: $($parseErrors[0].Message)" }
$repoRoot = [IO.Path]::GetFullPath((Join-Path $courseRoot '../../..'))
$runRoot = Join-Path $repoRoot ('.cache/course-copy-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $runRoot | Out-Null
$learner = Join-Path $runRoot 'learner'
function Assert([bool]$Condition, [string]$Message) { if (-not $Condition) { throw $Message } }
function Reject([scriptblock]$Action, [string]$Pattern) {
    $caught = $null
    try { & $Action | Out-Null } catch { $caught = $_.Exception.Message }
    Assert ($null -ne $caught -and $caught -match $Pattern) "Expected rejection '$Pattern'; got '$caught'"
}
Reject { & $scriptPath -Stage z02 -Destination $learner } 'starts at z01'
& $scriptPath -Stage z01 -Destination $learner
& $scriptPath -Stage z01 -Destination $learner
Reject { & $scriptPath -Stage z03 -Destination $learner } 'do not skip'
$collision = Join-Path $learner 'cmd/entity/main.go'
New-Item -ItemType Directory -Force -Path (Split-Path $collision -Parent) | Out-Null
[IO.File]::WriteAllText($collision, 'student owned file')
Reject { & $scriptPath -Stage z02 -Destination $learner } 'untracked learner file'
Assert ([IO.File]::ReadAllText($collision) -eq 'student owned file') 'Untracked collision overwritten'
# Remove only this test-created file whose exact contents were just checked.
[IO.File]::Delete($collision)
$notes = Join-Path $learner 'my-notes.txt'
[IO.File]::WriteAllText($notes, 'learner notes')
$modulePath = Join-Path $learner 'go.mod'
$original = [IO.File]::ReadAllBytes($modulePath)
[IO.File]::AppendAllText($modulePath, "`n// learner edit`n")
Reject { & $scriptPath -Stage z02 -Destination $learner } 'Learner edits detected'
Assert ([IO.File]::ReadAllText($modulePath).Contains('learner edit')) 'Learner edit overwritten'
Assert (-not (Test-Path (Join-Path $learner 'cmd/entity/main.go'))) 'Rejected update wrote partial new files'
[IO.File]::WriteAllBytes($modulePath, $original)
& $scriptPath -Stage z02 -Destination $learner
Assert ([IO.File]::ReadAllText($notes) -eq 'learner notes') 'Untracked notes lost'
Assert (@(Get-ChildItem (Join-Path $learner '.course/backups') -File -Recurse).Count -gt 0) 'Changed original files were not backed up'
Reject { & $scriptPath -Stage z01 -Destination (Join-Path $courseRoot 'lessons/new-learner') } 'course material'
Reject { & $scriptPath -Stage z01 -Destination (Join-Path $repoRoot 'cmd/new-learner') } 'outside repository source'
$recordPath = Join-Path $learner '.course/checkpoint.json'
$recordBytes = [IO.File]::ReadAllBytes($recordPath)
$record = Get-Content -LiteralPath $recordPath -Raw | ConvertFrom-Json
$record.files[0].path = '../outside.txt'
[IO.File]::WriteAllText($recordPath, ($record | ConvertTo-Json -Depth 5))
Reject { & $scriptPath -Stage z03 -Destination $learner } 'escapes'
[IO.File]::WriteAllBytes($recordPath, $recordBytes)
$outside = Join-Path $runRoot 'outside'
$junction = Join-Path $runRoot 'junction'
New-Item -ItemType Directory -Path $outside | Out-Null
New-Item -ItemType Junction -Path $junction -Target $outside | Out-Null
Reject { & $scriptPath -Stage z01 -Destination (Join-Path $junction 'nested/learner') } 'symbolic link or junction'
Assert (-not (Test-Path (Join-Path $outside 'nested'))) 'Wrote through a junction ancestor'
if ($AllStages) {
    foreach ($stage in @('z03','z04','z05','z06','z07','z08','z09','z10','z11','z12','z13')) { & $scriptPath -Stage $stage -Destination $learner }
    & $scriptPath -Stage z13 -Destination $learner
    Assert (Test-Path (Join-Path $learner 'cmd/search/main.go')) 'Search service missing from final checkpoint'
    Assert (Test-Path (Join-Path $learner 'cmd/web-gateway/main.go')) 'Browser gateway missing from final checkpoint'
    Assert (Test-Path (Join-Path $learner 'web/package-lock.json')) 'Reproducible frontend dependencies missing'
    Assert (Test-Path (Join-Path $learner 'compose.demo.yml')) 'Full demo deployment missing'
    Assert (-not (Test-Path (Join-Path $learner 'web/node_modules'))) 'Installed frontend cache leaked into checkpoint'
    Assert (-not (Test-Path (Join-Path $learner 'internal/tasks/integration-results.txt'))) 'Transient test output leaked into checkpoint'
    Assert (-not (Test-Path (Join-Path $learner 'cmd/course/main.go'))) 'Obsolete course CLI still present'
    Assert (Test-Path (Join-Path $learner 'cmd/verify/main.go')) 'Final verification command missing'
    Assert ([IO.File]::ReadAllText($notes) -eq 'learner notes') 'Notes lost after all stages'
    Assert (-not (Test-Path (Join-Path $learner 'docs'))) 'Repository documentation leaked into checkpoint'
    Assert (-not (Test-Path (Join-Path $learner '.git'))) 'Repository Git metadata leaked into checkpoint'
    Assert (-not (Test-Path (Join-Path $learner '.local'))) 'Local credentials leaked into checkpoint'
}
"PASS: checkpoint parsing, transitions, idempotence, edit protection, backup, path boundaries; learner=$learner"
