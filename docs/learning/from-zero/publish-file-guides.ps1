param([switch]$Check)
$ErrorActionPreference = 'Stop'
$guideRoot = Join-Path $PSScriptRoot 'lessons/files'
$index = Get-Content -LiteralPath (Join-Path $PSScriptRoot 'checkpoint-index.json') -Raw | ConvertFrom-Json
$utf8 = [Text.UTF8Encoding]::new($false)
if (-not $Check) { [IO.Directory]::CreateDirectory($guideRoot) | Out-Null }
$total = 0
foreach ($stage in $index.stages) {
    $lines = [Collections.Generic.List[string]]::new()
    $lines.Add("# $($stage.stage.ToUpper()) 完整文件清单与复制答案")
    $lines.Add('')
    $lines.Add('本页由阶段源码生成，验证范围以课程工作记录为准。每个代码块都是该路径的完整文件，覆盖时整份替换；不要把 Markdown 围栏写进源文件。所有相对路径均以同一个学习目录为根。文件创建到一半时暂时不能编译是正常的，阶段清单完成后再进行本阶段运行检查。')
    $lines.Add('')
    $lines.Add('手工路线：先停止上阶段服务；备份需要移除的旧文件到工程外，再移除清单列出的单个文件；按本页创建或替换文件。未列为变化的文件保留。受管理路线使用 apply-checkpoint.ps1，它完成相同的阶段文件变化；不要在没有阶段记录的手工目录调用它。')
    $lines.Add('')
    $lines.Add('## 移除的旧文件')
    $lines.Add('')
    $removed = @($stage.changes.removed)
    if ($removed.Count -eq 0) { $lines.Add('无。') }
    foreach ($path in $removed) { $lines.Add('- `' + $path + '`') }
    $lines.Add('')
    $lines.Add('## 本阶段全部文件')
    $lines.Add('')
    $lines.Add('| 路径 | 操作 | 完整原文件 |')
    $lines.Add('| --- | --- | --- |')
    foreach ($file in $stage.files) {
        $kind = if ($stage.stage -eq 'z01' -or $file.path -in $stage.changes.added) {'创建'} elseif ($file.path -in $stage.changes.replaced) {'替换'} else {'保留'}
        $url = '../../' + $stage.source + '/' + $file.path
        $lines.Add('| `' + $file.path + '` | ' + $kind + ' | [完整文件](' + $url + ') |')
    }
    $lines.Add('')
    $lines.Add('## 创建或替换的完整内容')
    $lines.Add('')
    foreach ($file in $stage.files) {
        if ($stage.stage -ne 'z01' -and $file.path -notin $stage.changes.added -and $file.path -notin $stage.changes.replaced) { continue }
        $source = Join-Path $PSScriptRoot ($stage.source + '/' + $file.path)
        if ((Get-FileHash -LiteralPath $source -Algorithm SHA256).Hash.ToLowerInvariant() -cne $file.sha256) { throw "Stage index is stale: $source" }
        $lines.Add('### `' + $file.path + '`')
        $lines.Add('')
        if ($file.path.StartsWith('gen/') -or $file.path.StartsWith('verification/') -or $file.path.EndsWith('.bin')) {
            $lines.Add('<!-- linked-file:' + $file.path + ' -->')
            $reason = if ($file.path.EndsWith('.bin')) {'这是二进制协议基线，必须直接复制表中的原文件，不能用文本编辑器保存或从代码块还原。'} elseif ($file.path.StartsWith('gen/')) {'这是生成文件。复制表中的完整原文件，或按本阶段生成脚本生成；不要手写。'} else {'这是已有验收记录，供查阅与复现比较，不是新代码，也不能当作你本机已经运行通过的证据。若需要完全一致的阶段目录，复制表中的原文件。'}
            $lines.Add($reason)
        } else {
            $code = [IO.File]::ReadAllText($source).Replace("`r`n", "`n")
            $language = switch ([IO.Path]::GetExtension($source)) { '.go' {'go'} '.proto' {'protobuf'} '.ps1' {'powershell'} '.sql' {'sql'} '.yaml' {'yaml'} '.json' {'json'} '.md' {'markdown'} default {'text'} }
            # 长围栏容纳 README 中自身的代码围栏。
            if ($code.Contains('`````')) { throw "Unsupported nested fence: $source" }
            $lines.Add('<!-- file:' + $file.path + ' -->')
            $lines.Add('`````' + $language)
            $lines.Add($code.TrimEnd("`n"))
            $lines.Add('`````')
            $lines.Add('<!-- end-file -->')
            $total++
        }
        $lines.Add('')
    }
    $text = ($lines -join "`n") + "`n"
    $destination = Join-Path $guideRoot ($stage.stage + '.md')
    if ($Check) {
        if (-not (Test-Path -LiteralPath $destination) -or [IO.File]::ReadAllText($destination) -cne $text) { throw "File guide differs: $destination" }
    } else { [IO.File]::WriteAllText($destination, $text, $utf8) }
}
Write-Output "Verified $total complete changed-file blocks across $($index.stages.Count) stage guides (check=$Check)."
