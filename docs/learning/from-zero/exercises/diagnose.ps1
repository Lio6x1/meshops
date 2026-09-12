param([Parameter(Mandatory=$true)][string]$Project)
$ErrorActionPreference = 'Stop'
# 本脚本只查询已运行的课程服务，不初始化数据库、不停启依赖。
$debugProject = (Resolve-Path -LiteralPath $Project).Path
$debugBinary = Join-Path $debugProject 'bin/opctl.exe'
if (-not (Test-Path -LiteralPath $debugBinary -PathType Leaf)) { throw 'Build the course project first.' }
foreach ($name in @('MESHOPS_OPERATOR_TOKEN','MESHOPS_ADMIN_TOKEN')) {
    if ([string]::IsNullOrEmpty([Environment]::GetEnvironmentVariable($name,'Process'))) { throw "Load this project scripts/env.ps1 first: $name is missing" }
}
$debugOutput = Join-Path $debugProject ('.local/debug-exercises/' + [guid]::NewGuid().ToString('N'))
[IO.Directory]::CreateDirectory($debugOutput) | Out-Null
$debugResults = [Collections.Generic.List[object]]::new()
$priorBadToken = [Environment]::GetEnvironmentVariable('MESHOPS_DEBUG_BAD_TOKEN','Process')
# 固定无效测试值，长度足够越过客户端长度检查，从而真正测试服务端认证。
[Environment]::SetEnvironmentVariable('MESHOPS_DEBUG_BAD_TOKEN', 'invalid-debug-credential-000000000000000000000000', 'Process')
# PowerShell 7用户可能启用了原生命令非零自动抛错；本脚本显式检查预期错误。
$PSNativeCommandUseErrorActionPreference = $false
function Invoke-DebugCase([string]$Name,[string[]]$Arguments,[int]$ExpectedExit,[string]$ExpectedCode,[string]$ExpectedText) {
    $stdout = Join-Path $debugOutput ($Name + '.out.txt')
    $stderr = Join-Path $debugOutput ($Name + '.err.txt')
    & $debugBinary @Arguments 1> $stdout 2> $stderr
    $exitCode = $LASTEXITCODE
    $errorText = [IO.File]::ReadAllText($stderr)
    if ($exitCode -ne $ExpectedExit) { throw "$Name expected exit $ExpectedExit, got $exitCode; see $stderr" }
    if ($ExpectedCode) {
        $payload = $errorText | ConvertFrom-Json
        if ($payload.error.code -cne $ExpectedCode) { throw "$Name expected $ExpectedCode; see $stderr" }
    }
    if ($ExpectedText -and -not $errorText.Contains($ExpectedText)) { throw "$Name missing expected diagnostic; see $stderr" }
    if ($ExpectedExit -eq 0) {
        $payload = Get-Content -LiteralPath $stdout -Raw | ConvertFrom-Json
        if ($null -eq $payload) { throw "$Name returned no response JSON" }
    }
    $debugResults.Add([ordered]@{name=$Name;exit=$exitCode;expectedCode=$ExpectedCode;passed=$true})
    Write-Output "PASS $Name exit=$exitCode code=$ExpectedCode"
}
Push-Location $debugProject
try {
    # 初始正确查询不要求实体已上报：found=false也是合法响应。
    Invoke-DebugCase '00-baseline' @('snapshot','--entity','drone-001') 0 '' ''
    Invoke-DebugCase '01-missing-argument' @('snapshot') 2 '' '--entity required'
    Invoke-DebugCase '02-bad-credential' @('snapshot','--entity','drone-001','--token-env','MESHOPS_DEBUG_BAD_TOKEN') 1 'Unauthenticated' 'invalid credential'
    Invoke-DebugCase '03-role-denied' @('dispatcher','status','--token-env','MESHOPS_OPERATOR_TOKEN') 1 'PermissionDenied' 'role cannot call method'
    Invoke-DebugCase '04-invalid-id' @('snapshot','--entity','BAD ID!') 1 'InvalidArgument' ''
    Invoke-DebugCase '05-correct-query' @('snapshot','--entity','drone-001') 0 '' ''
    Invoke-DebugCase '06-correct-role' @('dispatcher','status','--token-env','MESHOPS_ADMIN_TOKEN') 0 '' ''
    $result = [ordered]@{passed=$true;cases=$debugResults;scope='read-only CLI and real Entity/Dispatcher RPC; no task creation or dependency fault injection'}
} catch {
    $result = [ordered]@{passed=$false;cases=$debugResults;failure=$_.Exception.Message}
    throw
} finally {
    [Environment]::SetEnvironmentVariable('MESHOPS_DEBUG_BAD_TOKEN',$priorBadToken,'Process')
    Pop-Location
    $result | ConvertTo-Json -Depth 6 | Set-Content -LiteralPath (Join-Path $debugOutput 'result.json') -Encoding utf8
    Write-Output "Saved diagnostics: $debugOutput"
}
