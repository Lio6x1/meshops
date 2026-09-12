param([switch]$Check)
$ErrorActionPreference = 'Stop'
# 教师维护工具：正文代码直接取自已验收的阶段文件，避免维护两份答案。
$materialRoot = $PSScriptRoot
$utf8 = [System.Text.UTF8Encoding]::new($false)
$count = 0
foreach ($stage in @('z01', 'z02', 'z03')) {
    $lessonPath = Join-Path $materialRoot "lessons/$stage.md"
    $original = [IO.File]::ReadAllText($lessonPath).Replace("`r`n", "`n")
    $pattern = '(?s)<!-- source:([^\r\n]+?) -->(?:\n<!-- generated-code:start -->.*?<!-- generated-code:end -->)?'
    $rendered = [regex]::Replace($original, $pattern, [System.Text.RegularExpressions.MatchEvaluator]{
        param($match)
        $relative = $match.Groups[1].Value
        if ($relative -match '(^/|\\|(^|/)\.\.(/|$)|:)') { throw "Unsafe source path: $relative" }
        $source = Join-Path $materialRoot "starter/$stage/$relative"
        $code = [IO.File]::ReadAllText($source).Replace("`r`n", "`n").TrimEnd("`n")
        $lang = switch ([IO.Path]::GetExtension($source)) {
            '.go' {'go'} '.proto' {'protobuf'} '.ps1' {'powershell'} '.yaml' {'yaml'} default {'text'}
        }
        if ($code.Contains('```')) { throw "Source contains Markdown fence: $relative" }
        $script:count++
        return $match.Groups[0].Value.Split([string[]]@("`n"), [StringSplitOptions]::None)[0] +
            "`n<!-- generated-code:start -->`n" + '```' + $lang + "`n" + $code + "`n" +
            '```' + "`n<!-- generated-code:end -->"
    })
    if ($Check) {
        if ($original -cne $rendered) { throw "Lesson code differs from stage source: $lessonPath" }
    } else { [IO.File]::WriteAllText($lessonPath, $rendered, $utf8) }
}
if ($count -ne 15) { throw "Expected 15 source blocks, found $count" }
Write-Output "Verified $count complete source blocks (check=$Check)."
