param([switch]$Simulators,[switch]$Search)
$ErrorActionPreference = 'Stop'
$courseRoot = Split-Path $PSScriptRoot -Parent
. (Join-Path $PSScriptRoot 'processes.ps1')
$recordPath = Join-Path $courseRoot '.local/processes.json'
if (Test-Path -LiteralPath $recordPath) {
    $old = @(Get-Content -LiteralPath $recordPath -Raw | ConvertFrom-Json)
    foreach ($record in $old) {
        if (Get-CourseOwnedProcess $record) { throw 'This course run is already active. Stop it using scripts/stop.ps1 first.' }
    }
}
$roles = @('entity','task','dispatcher','ingest')
if ($Search) { $roles += 'search' }
$servicePorts = @(Get-CourseServicePorts $courseRoot $roles)
Assert-CoursePortsAvailable $servicePorts
$binaries = @($roles)
if ($Simulators) { $binaries += @('gateway-simulator','executor-simulator') }
foreach ($binary in $binaries) {
    if (-not (Test-Path -LiteralPath (Join-Path $courseRoot "bin/$binary.exe") -PathType Leaf)) { throw "Missing bin/$binary.exe; run scripts/build.ps1 first." }
}
# 上面的预检只读执行，并且早于 env.ps1 创建本地凭证。
if ($Search) { . (Join-Path $PSScriptRoot 'search-env.ps1') } else { . (Join-Path $PSScriptRoot 'env.ps1') }
$script:records = @()
New-Item -ItemType Directory -Force -Path (Join-Path $courseRoot 'data'),(Join-Path $courseRoot '.local/logs') | Out-Null
function Start-CourseProcess([string]$Name,[string]$Binary,[string[]]$Arguments) {
    $path = Join-Path $courseRoot "bin/$Binary.exe"
    $quoted = @($Arguments | ForEach-Object { '"' + $_.Replace('"','\"') + '"' })
    $process = Start-Process -FilePath $path -ArgumentList $quoted -WorkingDirectory $courseRoot -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $courseRoot ".local/logs/$Name.out.log") -RedirectStandardError (Join-Path $courseRoot ".local/logs/$Name.err.log")
    $script:records += New-CourseProcessRecord $Name $process
    ConvertTo-Json -InputObject @($script:records) | Set-Content -LiteralPath $recordPath -Encoding utf8
    Assert-CourseProcessesAlive $script:records
}
try {
    foreach ($role in $roles) { Start-CourseProcess $role $role @('-f',"configs/$role.yaml") }
    foreach ($servicePort in @($servicePorts | Where-Object field -eq 'MetricsAddr')) {
        $port = $servicePort.port
        $limit = [DateTime]::UtcNow.AddSeconds(40)
        $ok = $false
        while ([DateTime]::UtcNow -lt $limit) {
            Assert-CourseProcessesAlive $script:records
            try { $r=Invoke-WebRequest "http://127.0.0.1:$port/readyz" -UseBasicParsing -TimeoutSec 3; if ($r.StatusCode -eq 200) { $ok=$true;break } } catch { }
            Start-Sleep -Milliseconds 250
        }
        Assert-CourseProcessesAlive $script:records
        if (-not $ok) { throw "Service at metrics port $port is not ready; see .local/logs" }
    }
    if ($Simulators) {
        $env:MESHOPS_SIMULATION_CONTROL = "1"
        foreach ($source in @('personnel_sim','drone_sim','vehicle_sim','robot_sim','sensor_sim','facility_sim')) {
            Start-CourseProcess $source 'gateway-simulator' @('--manifest','configs/simulation.yaml','--source',$source,'--db',"data/$source.db",'--rate','2','--duration','30m')
        }
        foreach ($executor in @('simulated_personnel','simulated_aircraft','simulated_vehicle','simulated_robot')) {
            Start-CourseProcess $executor 'executor-simulator' @('--manifest','configs/simulation.yaml','--executor',$executor,'--db',"data/$executor.db")
        }
    }
    Start-Sleep -Milliseconds 250
    Assert-CourseProcessesAlive $script:records
    Write-Output 'Course processes started; logs: .local/logs; stop: ./scripts/stop.ps1'
} catch {
    $startupFailure = $_
    if ($script:records.Count) { ConvertTo-Json -InputObject @($script:records) | Set-Content -LiteralPath (Join-Path $courseRoot '.local/failed-start-records.json') -Encoding utf8 }
    $remaining = @()
    foreach ($record in $script:records) {
        try { Stop-CourseOwnedProcess $record }
        catch { $remaining += $record; Write-Warning "Startup cleanup failed: $($_.Exception.Message)" }
    }
    if ($script:records.Count) { ConvertTo-Json -InputObject @($remaining) | Set-Content -LiteralPath $recordPath -Encoding utf8 }
    throw $startupFailure
}
