# Publish only the root runtime as Z10. Earlier teaching snapshots stay frozen.
param([switch]$Check)
$ErrorActionPreference = 'Stop'
$repo = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot '../../..'))
$path = Join-Path $PSScriptRoot 'checkpoint-index.json'
$index = Get-Content -LiteralPath $path -Raw | ConvertFrom-Json
$previous = @($index.stages | Where-Object stage -eq 'z09')
if ($previous.Count -ne 1) { throw 'Exactly one frozen Z09 is required.' }
$candidates = [Collections.Generic.List[string]]::new()
foreach ($name in @('.gitattributes','.gitignore','go.mod','go.sum','README.md','docker-compose.yml','compose.search.yml')) { $candidates.Add($name) }
foreach ($directory in @('.github','cmd','configs','gen','internal','migrations','proto','scripts','testdata')) {
    foreach ($file in Get-ChildItem -LiteralPath (Join-Path $repo $directory) -File -Recurse) {
        $relative = [IO.Path]::GetRelativePath($repo,$file.FullName).Replace('\','/')
        if ($relative -match '(^|/)integration-results\.txt$') { continue }
        if ($relative -match '(^|/)__pycache__(/|$)|\.py[cod]$') { continue }
        if ($relative -match '(^|/)(\.local|\.cache|bin|data|results)(/|$)|\.(exe|test)$') { throw "Runtime artifact in published source: $relative" }
        if ($file.Attributes -band [IO.FileAttributes]::ReparsePoint) { throw "Linked source file: $relative" }
        $candidates.Add($relative)
    }
}
# Preserve the historical protocol/verification reference already in Z09; do
# not collect new local results or turn them into learner success claims.
foreach ($file in $previous[0].files | Where-Object path -like 'verification/*') { $candidates.Add($file.path) }
$files = @(foreach ($relative in @($candidates | Sort-Object -Unique)) {
    [ordered]@{path=$relative;sha256=(Get-FileHash -LiteralPath (Join-Path $repo $relative) -Algorithm SHA256).Hash.ToLowerInvariant()}
})
$old = @{}
foreach ($file in $previous[0].files) { $old[$file.path]=$file.sha256 }
$stage = [ordered]@{
    stage='z10'; source='../../..'; files=$files
    changes=[ordered]@{
        added=@($files | Where-Object { -not $old.ContainsKey($_.path) } | ForEach-Object path)
        removed=@($previous[0].files | Where-Object path -NotIn $files.path | ForEach-Object path)
        replaced=@($files | Where-Object { $old.ContainsKey($_.path) -and $old[$_.path] -cne $_.sha256 } | ForEach-Object path)
    }
}
if ($Check) {
    $published = @($index.stages | Where-Object stage -eq 'z10')
    if ($published.Count -ne 1 -or ($published[0] | ConvertTo-Json -Depth 10 -Compress) -cne ($stage | ConvertTo-Json -Depth 10 -Compress)) { throw 'Z10 index differs from root runtime.' }
} else {
    $index.stages = @($index.stages | Where-Object stage -ne 'z10') + @($stage)
    $index.date = '2026-09-12'
    [IO.File]::WriteAllText($path,($index | ConvertTo-Json -Depth 10)+"`n",[Text.UTF8Encoding]::new($false))
}
Write-Output "Z10: $($files.Count) files; $($stage.changes.added.Count) additions; $($stage.changes.replaced.Count) replacements; check=$Check."
