# 独立进程测试，不读取学习工程的 processes.json，也不停止学习服务。
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'processes.ps1')
function Assert-True([bool]$Condition, [string]$Message) {
    if (-not $Condition) { throw $Message }
}
function Assert-Throws([scriptblock]$Action, [string]$Message) {
    $failed = $false
    try { & $Action } catch { $failed = $true }
    Assert-True $failed $Message
}

$child = $null
$record = $null
$temporaryRecord = [IO.Path]::GetTempFileName()
$listener = New-Object Net.Sockets.TcpListener([Net.IPAddress]::Loopback, 0)
try {
    $shellPath = (Get-Process -Id $PID).Path
    $child = Start-Process -FilePath $shellPath -ArgumentList @('-NoProfile', '-NonInteractive', '-Command', 'Start-Sleep -Seconds 60') -WindowStyle Hidden -PassThru
    $record = New-CourseProcessRecord 'isolated-process-test' $child
    $record | ConvertTo-Json | Set-Content -LiteralPath $temporaryRecord -Encoding utf8
    $decoded = Get-Content -LiteralPath $temporaryRecord -Raw | ConvertFrom-Json
    Assert-True ($null -ne (Get-CourseOwnedProcess $decoded)) 'Tick-string JSON roundtrip lost ownership.'
    Assert-CourseProcessesAlive @($decoded)

    $legacy = [PSCustomObject]@{ name = $record.name; pid = $record.pid; path = $record.path; started = $record.started }
    Assert-True ($null -ne (Get-CourseOwnedProcess $legacy)) 'Legacy ISO string lost ownership.'
    $legacy | ConvertTo-Json | Set-Content -LiteralPath $temporaryRecord -Encoding utf8
    $legacyDecoded = Get-Content -LiteralPath $temporaryRecord -Raw | ConvertFrom-Json
    Assert-True ($null -ne (Get-CourseOwnedProcess $legacyDecoded)) 'Legacy ISO JSON roundtrip lost ownership.'
    $legacy.started = $child.StartTime.ToUniversalTime()
    Assert-True ($null -ne (Get-CourseOwnedProcess $legacy)) 'DateTime legacy record lost ownership.'
    $legacy.started = [DateTimeOffset]$child.StartTime.ToUniversalTime()
    Assert-True ($null -ne (Get-CourseOwnedProcess $legacy)) 'DateTimeOffset legacy record lost ownership.'

    $wrong = [PSCustomObject]@{ name = 'wrong-start'; pid = $record.pid; path = $record.path; startedTicks = ([int64]$record.startedTicks + 1).ToString() }
    Assert-True ($null -eq (Get-CourseOwnedProcess $wrong)) 'Different start ticks matched the PID.'
    Stop-CourseOwnedProcess $wrong
    Assert-CourseProcessesAlive @($record)
    $wrong.startedTicks = $record.startedTicks
    $wrong.path = Join-Path ([IO.Path]::GetTempPath()) 'not-the-child.exe'
    Stop-CourseOwnedProcess $wrong
    Assert-CourseProcessesAlive @($record)
    $wrong.path = $record.path
    $wrong.startedTicks = 'malformed'
    Stop-CourseOwnedProcess $wrong
    Assert-CourseProcessesAlive @($record)

    $ports = @(Get-CourseServicePorts (Split-Path $PSScriptRoot -Parent))
    Assert-True ($ports.Count -eq 8) 'Expected four RPC and four metrics ports from configuration.'
    $listener.Start()
    $temporaryPort = $listener.LocalEndpoint.Port
    $portRecord = [PSCustomObject]@{ role = 'isolated-test'; field = 'ListenOn'; port = $temporaryPort }
    Assert-Throws { Assert-CoursePortsAvailable @($portRecord) } 'An occupied TCP port passed preflight.'
    $listener.Stop()
    Assert-CoursePortsAvailable @($portRecord)
    Assert-Throws { Assert-CoursePortsAvailable @($portRecord, $portRecord) } 'Duplicate configured ports passed preflight.'

    Stop-CourseOwnedProcess $legacyDecoded 5000
    Assert-True ($child.WaitForExit(5000)) 'Owned dummy child did not terminate.'
    Assert-True ($null -eq (Get-CourseOwnedProcess $record)) 'Exited child still matched ownership.'
    Assert-Throws { Assert-CourseProcessesAlive @($record) } 'Exited child passed readiness ownership check.'
    Stop-CourseOwnedProcess $record
    Write-Output "Process helper tests passed on PowerShell $($PSVersionTable.PSVersion): identity formats, rejected records, port preflight, bounded stop, child exit."
} finally {
    $listener.Stop()
    if ($record) { Stop-CourseOwnedProcess $record 5000 }
    elseif ($child -and -not $child.HasExited) { Stop-Process -InputObject $child -Force; $null = $child.WaitForExit(5000) }
    Remove-Item -LiteralPath $temporaryRecord -Force
}
