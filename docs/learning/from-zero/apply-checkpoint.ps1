param(
    [Parameter(Mandatory=$true)][ValidateSet('z01','z02','z03','z04','z05','z06','z07','z08','z09','z10','z11','z12','z13')][string]$Stage,
    [Parameter(Mandatory=$true)][string]$Destination
)
$ErrorActionPreference = 'Stop'
$published = Get-Content -LiteralPath (Join-Path $PSScriptRoot 'checkpoint-index.json') -Raw | ConvertFrom-Json
$selected = @($published.stages | Where-Object stage -eq $Stage)
if ($selected.Count -ne 1) { throw "Checkpoint $Stage has not been published yet." }
$source = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot $selected[0].source))
$target = [IO.Path]::GetFullPath($Destination)
$separator = [IO.Path]::DirectorySeparatorChar
$sourcePrefix = $source.TrimEnd('\','/') + $separator
$targetPrefix = $target.TrimEnd('\','/') + $separator
$materialRoot = [IO.Path]::GetFullPath($PSScriptRoot).TrimEnd('\','/')
if ($target -eq $materialRoot -or $target.StartsWith($materialRoot+$separator,[StringComparison]::OrdinalIgnoreCase) -or $materialRoot.StartsWith($targetPrefix,[StringComparison]::OrdinalIgnoreCase)) { throw 'The learner directory must be separate from all course material.' }
$repositoryRoot = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../../..')).TrimEnd('\','/')
$scratchPrefix = (Join-Path $repositoryRoot '.cache') + $separator
$scratchTarget = $target.StartsWith($scratchPrefix,[StringComparison]::OrdinalIgnoreCase)
if (($target -eq $repositoryRoot -or $target.StartsWith($repositoryRoot+$separator,[StringComparison]::OrdinalIgnoreCase)) -and -not $scratchTarget) { throw 'The learner directory must be outside repository source; only .cache test directories are allowed inside.' }
if ($target -eq $source -or ($target.StartsWith($sourcePrefix,[StringComparison]::OrdinalIgnoreCase) -and -not ($source -eq $repositoryRoot -and $scratchTarget)) -or $source.StartsWith($targetPrefix,[StringComparison]::OrdinalIgnoreCase)) { throw 'The learner directory and reference directory must be separate.' }
if (-not (Test-Path -LiteralPath (Join-Path $source 'go.mod'))) { throw "Checkpoint $Stage has not been published yet." }
# A nonexistent leaf can still have a junction in its existing parent chain.
foreach ($rootPath in @($source,$target)) {
    $ancestor = $rootPath
    while ($ancestor) {
        if (Test-Path -LiteralPath $ancestor) {
            if ((Get-Item -LiteralPath $ancestor -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) { throw "Refusing a symbolic link or junction: $ancestor" }
        }
        $ancestor = [IO.Path]::GetDirectoryName($ancestor)
    }
}

function Resolve-ContainedPath([string]$Root,[string]$Relative) {
    if ([IO.Path]::IsPathRooted($Relative)) { throw 'Checkpoint paths must be relative.' }
    $rootFull=[IO.Path]::GetFullPath($Root).TrimEnd('\','/')
    $full=[IO.Path]::GetFullPath((Join-Path $rootFull $Relative))
    if (-not $full.StartsWith($rootFull+$separator,[StringComparison]::OrdinalIgnoreCase)) { throw "Path escapes checkpoint directory: $Relative" }
    $current=$full
    while ($current -and $current.Length -ge $rootFull.Length) {
        if (Test-Path -LiteralPath $current) {
            $item=Get-Item -LiteralPath $current -Force
            if ($item.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw "Refusing a symbolic link or junction: $current" }
        }
        if ($current -eq $rootFull) { break }
        $current=[IO.Path]::GetDirectoryName($current)
    }
    return $full
}
function Hash-File([string]$Path) { return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant() }
function Include-SourceFile([string]$Relative) {
    $parts=$Relative -split '[\\/]'
    foreach ($part in $parts) { if ($part -in @('.git','.local','.course','bin','data','results','.cache')) { return $false } }
    return -not ($Relative -match '(\.exe$|\.test$|integration-results\.txt$)')
}

$statePath=Resolve-ContainedPath $target '.course/checkpoint.json'
$old=@{}
if (Test-Path -LiteralPath $statePath) {
    $state=Get-Content -LiteralPath $statePath -Raw | ConvertFrom-Json
    if ($state.module -ne 'example.com/meshops-course') { throw 'Unrecognized checkpoint record.' }
    if ($state.stage -notmatch '^z(0[1-9]|1[0-3])$') { throw 'Invalid checkpoint stage in record.' }
    $oldStage=[int]$state.stage.Substring(1)
    $newStage=[int]$Stage.Substring(1)
    if ($newStage -ne $oldStage -and $newStage -ne $oldStage+1) { throw "Apply $($state.stage) again or its next stage; do not skip a stage." }
    foreach ($file in $state.files) {
        $null = Resolve-ContainedPath $target $file.path
        if ($old.ContainsKey($file.path) -or $file.sha256 -notmatch '^[a-f0-9]{64}$') { throw 'Invalid or duplicate checkpoint file record.' }
        $old[$file.path]=$file.sha256
    }
} else {
    if ($Stage -ne 'z01') { throw 'An empty learner project starts at z01.' }
    if ((Test-Path -LiteralPath $target) -and @(Get-ChildItem -LiteralPath $target -Force).Count) { throw 'The first checkpoint requires an empty directory; existing work is never overwritten.' }
}

# Prepare every path and check every existing file before the first write.
$incoming=@{}
$checkpoint = @((Get-Content (Join-Path $PSScriptRoot 'checkpoint-index.json') -Raw | ConvertFrom-Json).stages | Where-Object stage -eq $Stage)
if ($checkpoint.Count -ne 1) { throw 'Missing or duplicate checkpoint manifest.' }
# Copy only published files: Z10 lives beside repository docs and local data.
foreach ($file in $checkpoint[0].files) {
    $relative=$file.path
    if (-not (Include-SourceFile $relative) -or $incoming.ContainsKey($relative)) { throw "Invalid published source path: $relative" }
    $safeSource=Resolve-ContainedPath $source $relative
    $safeTarget=Resolve-ContainedPath $target $relative
    $hash=Hash-File $safeSource
    if ($hash -cne $file.sha256) { throw "Published source changed: $relative" }
    $incoming[$relative]=[PSCustomObject]@{path=$relative;sha256=$hash;source=$safeSource;target=$safeTarget}
    if ((Test-Path -LiteralPath $safeTarget) -and -not $old.ContainsKey($relative)) { throw "An untracked learner file would be overwritten: $relative" }
}
foreach ($relative in $old.Keys) {
    $path=Resolve-ContainedPath $target $relative
    if (-not (Test-Path -LiteralPath $path -PathType Leaf)) { throw "Tracked file missing: $relative. Restore it before applying the next checkpoint." }
    if ((Hash-File $path) -ne $old[$relative]) { throw "Learner edits detected: $relative. Save them separately or restore the previous reference version before applying." }
}

New-Item -ItemType Directory -Force -Path $target | Out-Null
$backup=Join-Path $target ('.course/backups/'+[DateTime]::UtcNow.ToString('yyyyMMddTHHmmssfffffff')+'-'+$Stage)
foreach ($relative in $old.Keys) {
    if (-not $incoming.ContainsKey($relative) -or $incoming[$relative].sha256 -ne $old[$relative]) {
        $saved=Resolve-ContainedPath $backup $relative
        New-Item -ItemType Directory -Force -Path ([IO.Path]::GetDirectoryName($saved)) | Out-Null
        Copy-Item -LiteralPath (Resolve-ContainedPath $target $relative) -Destination $saved
    }
}
foreach ($relative in $old.Keys) {
    if (-not $incoming.ContainsKey($relative)) {
        # Only an individually verified, tracked obsolete file is removed.
        $obsolete=Resolve-ContainedPath $target $relative
        Remove-Item -LiteralPath $obsolete
    }
}
foreach ($file in $incoming.Values) {
    New-Item -ItemType Directory -Force -Path ([IO.Path]::GetDirectoryName($file.target)) | Out-Null
    Copy-Item -LiteralPath $file.source -Destination $file.target -Force
    if ((Hash-File $file.target) -ne $file.sha256) { throw "Copy verification failed: $($file.path)" }
}
New-Item -ItemType Directory -Force -Path (Join-Path $target '.course') | Out-Null
$files=@($incoming.Values | Sort-Object path | Select-Object path,sha256)
@{stage=$Stage;module='example.com/meshops-course';files=$files} | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $statePath -Encoding utf8
Write-Output "Applied ${Stage}: $($files.Count) complete files. Learner directory: $target"
Write-Output 'Run the commands in this lesson from the learner directory. This copy step does not build or start services.'
