# 仅验证编排契约；下面的 docker 函数只记录参数，
# 不会调用 Docker 引擎、容器、数据卷或外部命令。
$ErrorActionPreference = 'Stop'
$testState = [pscustomobject]@{
    Calls = [Collections.Generic.List[object]]::new()
    FailRebuild = $false
    HTTPCalls = [Collections.Generic.List[string]]::new()
    HTTPFailure = ''
}
Set-Item Function:docker -Value ({
    $testState.Calls.Add([string[]]$args)
    $global:LASTEXITCODE = 0
    if ($testState.FailRebuild -and $args -contains 'rebuild-search') { $global:LASTEXITCODE = 1 }
}.GetNewClosure())
Set-Item Function:Invoke-WebRequest -Value ({
    param([string]$Uri,[switch]$UseBasicParsing,[int]$TimeoutSec)
    $testState.HTTPCalls.Add($Uri)
    if ($TimeoutSec -le 0 -or $TimeoutSec -gt 10) { throw 'HTTP probe must use a bounded timeout.' }
    if ($testState.HTTPFailure -eq 'connection') { throw 'Simulated host connection refused.' }
    if ($testState.HTTPFailure -eq 'page' -and $Uri.EndsWith('/')) { return [pscustomobject]@{ StatusCode = 503 } }
    return [pscustomobject]@{ StatusCode = 200 }
}.GetNewClosure())
function Assert-Demo([bool]$Condition,[string]$Message) { if (-not $Condition) { throw $Message } }
$previousExit = $global:LASTEXITCODE
try {
    & (Join-Path $PSScriptRoot 'demo-stack.ps1') -Action RebuildSearch
    $calls = @($testState.Calls | ForEach-Object { $_ -join ' ' })
    Assert-Demo ($calls.Count -eq 6) 'Unexpected successful recovery sequence.'
    Assert-Demo ($calls[0] -match ' stop search canal$') 'Recovery did not stop only its consumers first.'
    Assert-Demo ($calls[1] -match ' init invalidate-search$') 'Recovery did not invalidate old readiness.'
    Assert-Demo ($calls[2] -match ' init rebuild-search$') 'Missing snapshot rebuild.'
    Assert-Demo ($calls[3] -match ' rm -f canal$') 'Canal writable TSDB layer was not replaced after successful import.'
    Assert-Demo ($calls[4] -match ' canal reset-canal-meta$') 'Missing restricted metadata cleanup.'
    Assert-Demo ($calls[5] -match 'up -d --no-deps --force-recreate --wait --wait-timeout 180 canal search$') 'Wrong restart scope.'
    foreach ($call in $calls) {
        Assert-Demo ($call -match ' -p meshops-demo ') 'Unscoped project.'
        Assert-Demo ($call -notmatch '--volumes|--remove-orphans|\bseed\b') 'Recovery touched unrelated data.'
    }
    $testState.Calls.Clear()
    $testState.FailRebuild = $true
    $failed = $false
    try { & (Join-Path $PSScriptRoot 'demo-stack.ps1') -Action RebuildSearch } catch { $failed = $true }
    Assert-Demo $failed 'Failed import was hidden.'
    $calls = @($testState.Calls | ForEach-Object { $_ -join ' ' })
    Assert-Demo ($calls.Count -eq 5) 'Failure did not stop consumers and invalidate readiness again.'
    Assert-Demo ($calls[3] -match ' stop search canal$') 'Failure restarted consumers.'
    Assert-Demo ($calls[4] -match ' init invalidate-search$') 'Failure left a complete marker.'
    foreach ($call in $calls) { Assert-Demo ($call -notmatch ' reset-canal-meta$| rm -f | up ') 'Cleanup/start ran after a failed import.' }
    Write-Output 'Demo search recovery orchestration: success and failure paths PASS (no Docker calls).'

    $testState.FailRebuild = $false
    $testState.Calls.Clear()
    $upOutput = @(& (Join-Path $PSScriptRoot 'demo-stack.ps1') -Action Up -NoBuild 6>&1)
    Assert-Demo ($testState.HTTPCalls.Count -eq 2) 'Up did not probe both host HTTP endpoints.'
    Assert-Demo ($testState.HTTPCalls[0] -eq 'http://127.0.0.1:18090/healthz') 'Missing loopback gateway readiness probe.'
    Assert-Demo ($testState.HTTPCalls[1] -eq 'http://127.0.0.1:18090/') 'Missing frontend page probe.'
    Assert-Demo (($upOutput -join "`n") -match 'Demo is running:') 'Successful startup was not reported.'
    foreach ($failureMode in @('connection','page')) {
        $testState.Calls.Clear()
        $testState.HTTPCalls.Clear()
        $testState.HTTPFailure = $failureMode
        $failed = $false
        $upOutput = [Collections.Generic.List[object]]::new()
        try { & (Join-Path $PSScriptRoot 'demo-stack.ps1') -Action Up -NoBuild 6>&1 | ForEach-Object { $upOutput.Add($_) } }
        catch { $failed = $true; Assert-Demo ($_.Exception.Message -match 'host HTTP') 'Failure did not explain the host HTTP probe.' }
        Assert-Demo $failed "Healthy containers hid host HTTP $failureMode failure."
        Assert-Demo (($upOutput -join "`n") -notmatch 'Demo is running:') 'Failed host probe printed startup success.'
        foreach ($call in $testState.Calls) { Assert-Demo (($call -join ' ') -notmatch '\bdown\b|--volumes') 'Host probe failure destroyed the stack.' }
    }
    Write-Output 'Demo Up host HTTP probes: success, connection failure and non-200 page PASS (no network calls).'
} finally {
    Remove-Item Function:docker
    Remove-Item Function:Invoke-WebRequest
    $global:LASTEXITCODE = $previousExit
}
