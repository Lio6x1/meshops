#requires -Version 7.0
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'account-admin.ps1')

$testRoot = Split-Path $PSScriptRoot -Parent
$invocation = Get-AccountAdminInvocation -Mode Docker -Root $testRoot -Command bootstrap -Username admin -DisplayName '管理员'
if ($invocation.Arguments -notcontains '-T' -or $invocation.Arguments -notcontains 'meshops-demo' -or $invocation.Arguments -notcontains '/app/account-admin') { throw 'Docker invocation must use the dedicated project and non-TTY stdin.' }
if (($invocation.Arguments -join ' ') -match 'password|root:') { throw 'Credentials must not enter process arguments.' }
$local = Get-AccountAdminInvocation -Mode Local -Root $testRoot -Command status -Username admin -DisplayName '管理员'
if ($local.Path -ne (Join-Path $testRoot 'bin/account-admin.exe') -or $local.Arguments -notcontains 'status') { throw 'Local invocation uses the wrong program.' }

# 子进程真实读取标准输入，验证中文和空格不被 shell 改写，也不进入 argv。
$testDirectory = Join-Path $testRoot ('.cache/account-admin-test-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $testDirectory -Force | Out-Null
$fixture = Join-Path $testDirectory 'stdin fixture.ps1'
@'
[Console]::InputEncoding = [Text.UTF8Encoding]::new($false)
$body = [Console]::In.ReadToEnd() | ConvertFrom-Json
if ($args.Count -ne 0) { exit 4 }
if ($body.password -cne ' 测试Password-123! ') { exit 5 }
[Console]::Out.Write('{"accepted":true}')
'@ | Set-Content -LiteralPath $fixture -Encoding utf8
$probe = [pscustomobject]@{Path=(Get-Command pwsh).Source;Arguments=@('-NoProfile','-File',$fixture)}
$answer = Invoke-AccountAdminCommand -Invocation $probe -Payload (@{password=' 测试Password-123! '} | ConvertTo-Json -Compress)
if (-not $answer.accepted) { throw 'Password stdin roundtrip failed.' }

# 失败正文可能包含用户数据，包装层只给固定诊断，不能回显子进程输出。
'[Console]::Error.Write("fixture-private-value"); exit 9' | Set-Content -LiteralPath $fixture -Encoding utf8
$failed = $false
try { Invoke-AccountAdminCommand -Invocation $probe -Payload '{}' | Out-Null }
catch {
    $failed = $true
    if ($_.Exception.Message -match 'fixture-private-value') { throw 'Child stderr leaked through wrapper.' }
}
if (-not $failed) { throw 'Failed command appeared successful.' }
'[Console]::Out.Write("not-json")' | Set-Content -LiteralPath $fixture -Encoding utf8
$failed = $false
try { Invoke-AccountAdminCommand -Invocation $probe | Out-Null } catch { $failed=$true }
if (-not $failed) { throw 'Malformed command output accepted.' }
Write-Host 'PASS: isolated invocation, password stdin, secret-safe failure and strict JSON output.'
