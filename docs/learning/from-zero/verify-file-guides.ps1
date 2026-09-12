param([switch]$Build)
$ErrorActionPreference = 'Stop'
$materialRoot = $PSScriptRoot
$repoRoot = [IO.Path]::GetFullPath((Join-Path $materialRoot '../../..'))
$runRoot = Join-Path $repoRoot ('.cache/lesson-copy-' + [guid]::NewGuid().ToString('N'))
$learnerRoot = Join-Path $runRoot 'learner'
[IO.Directory]::CreateDirectory($learnerRoot) | Out-Null
$index = Get-Content -LiteralPath (Join-Path $materialRoot 'checkpoint-index.json') -Raw | ConvertFrom-Json
$results = [Collections.Generic.List[object]]::new()
$utf8 = [Text.UTF8Encoding]::new($false)
$env:GOWORK = 'off'
$env:GOCACHE = Join-Path $repoRoot '.cache/go-build-reference'
$go = 'D:/go1.25.10/bin/go.exe'
function LearnerPath([string]$relative) {
    if ($relative -match '(^/|\\|:|(^|/)\.\.(/|$))') { throw "Unsafe lesson path: $relative" }
    $path = [IO.Path]::GetFullPath((Join-Path $learnerRoot $relative))
    if (-not $path.StartsWith($learnerRoot + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) { throw "Path escapes learner: $path" }
    return $path
}
try {
    foreach ($stage in $index.stages) {
        $guide = [IO.File]::ReadAllText((Join-Path $materialRoot ('lessons/files/' + $stage.stage + '.md'))).Replace("`r`n", "`n")
        $blocks = @{}
        foreach ($match in [regex]::Matches($guide, '(?ms)^<!-- file:([^\n]+) -->\n`````[^\n]*\n(.*?)\n`````\n<!-- end-file -->')) {
            $path = $match.Groups[1].Value
            if ($blocks.ContainsKey($path)) { throw "Duplicate file block: $path" }
            $blocks[$path] = $match.Groups[2].Value + "`n"
        }
        $links = @{}
        foreach ($match in [regex]::Matches($guide, '(?m)^<!-- linked-file:([^\n]+) -->$')) { $links[$match.Groups[1].Value] = $true }
        foreach ($relative in $stage.changes.removed) {
            $target = LearnerPath $relative
            if (-not $guide.Contains('- `' + $relative + '`')) { throw "Missing removal instruction: $relative" }
            if (-not (Test-Path -LiteralPath $target -PathType Leaf)) { throw "Missing old file: $relative" }
            # Only individually checked files inside this newly allocated learner.
            Remove-Item -LiteralPath $target
        }
        $expectedChanges = @($stage.files | Where-Object { $stage.stage -eq 'z01' -or $_.path -in $stage.changes.added -or $_.path -in $stage.changes.replaced })
        if ($blocks.Count + $links.Count -ne $expectedChanges.Count) { throw "Guide changed-file count mismatch: $($stage.stage)" }
        foreach ($file in $expectedChanges) {
            $target = LearnerPath $file.path
            [IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName($target)) | Out-Null
            if ($blocks.ContainsKey($file.path)) { [IO.File]::WriteAllText($target, $blocks[$file.path], $utf8) }
            elseif ($links.ContainsKey($file.path)) {
                $relativeLink = '../../' + $stage.source + '/' + $file.path
                if (-not $guide.Contains('(' + $relativeLink + ')')) { throw "Missing source link: $relativeLink" }
                Copy-Item -LiteralPath (Join-Path $materialRoot ($stage.source + '/' + $file.path)) -Destination $target
            } else { throw "Required file has no complete answer: $($file.path)" }
        }
        foreach ($file in $stage.files) {
            $source = Join-Path $materialRoot ($stage.source + '/' + $file.path)
            if ((Get-FileHash -LiteralPath $source -Algorithm SHA256).Hash.ToLowerInvariant() -cne $file.sha256) { throw "Source changed: $source" }
            if ($file.path.EndsWith('.bin')) {
                if ((Get-FileHash -LiteralPath (LearnerPath $file.path) -Algorithm SHA256).Hash.ToLowerInvariant() -cne $file.sha256) { throw "Binary fixture differs: $($file.path)" }
                continue
            }
            $actual = [IO.File]::ReadAllText((LearnerPath $file.path)).Replace("`r`n", "`n").TrimEnd("`n")
            $expected = [IO.File]::ReadAllText($source).Replace("`r`n", "`n").TrimEnd("`n")
            if ($actual -cne $expected) { throw "Copied code mismatch: $($stage.stage)/$($file.path)" }
        }
        if ($Build) {
            Push-Location $learnerRoot
            try {
                & $go build ./... *> (Join-Path $runRoot ($stage.stage + '-build.txt'))
                if ($LASTEXITCODE -ne 0) { throw "Build failed: $($stage.stage)" }
                if ($stage.stage -in @('z02','z03')) {
                    & ./smoke.ps1 *> (Join-Path $runRoot ($stage.stage + '-smoke.txt'))
                }
            } finally { Pop-Location }
        }
        $results.Add([ordered]@{stage=$stage.stage; completeBlocks=$blocks.Count; linkedGeneratedOrEvidence=$links.Count; finalFiles=$stage.files.Count; sourceTextEqual=$true; built=[bool]$Build; smoke=($Build -and $stage.stage -in @('z02','z03'))})
        Write-Output "$($stage.stage): copied from Markdown, verified $($stage.files.Count) files."
    }
    $result = [ordered]@{passed=$true; learner=$learnerRoot; source='Markdown full-file blocks plus explicit generated/evidence links'; normalized='CRLF/LF and terminal newlines'; stages=$results; note='No new Docker fault or performance run; stage source equivalence checked.'}
} catch {
    $result = [ordered]@{passed=$false; learner=$learnerRoot; stages=$results; failure=$_.Exception.Message}
    throw
} finally {
    $result | ConvertTo-Json -Depth 8 | Set-Content -LiteralPath (Join-Path $runRoot 'result.json') -Encoding utf8
    Write-Output "Evidence: $runRoot"
}
