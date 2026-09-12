# 可向已运行的学习环境添加 Search，也可单独启动 Search。
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'search-env.ps1')
. (Join-Path $PSScriptRoot 'processes.ps1')
$recordPath = Join-Path $courseRoot '.local/processes.json'
$records = @()
if (Test-Path -LiteralPath $recordPath) { $records = @(Get-Content -LiteralPath $recordPath -Raw | ConvertFrom-Json) }
foreach ($record in @($records | Where-Object name -eq 'search')) {
    if (Get-CourseOwnedProcess $record) { throw 'Search is already running.' }
}
$ports = @(Get-CourseServicePorts $courseRoot @('search'))
Assert-CoursePortsAvailable $ports
$records = @($records | Where-Object name -ne 'search')
$owned = $null
try {
    $binary = Join-Path $courseRoot 'bin/search.exe'
    New-Item -ItemType Directory -Force -Path (Join-Path $courseRoot '.local/logs') | Out-Null
    $process = Start-Process -FilePath $binary -ArgumentList @('-f','configs/search.yaml') -WorkingDirectory $courseRoot -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $courseRoot '.local/logs/search.out.log') -RedirectStandardError (Join-Path $courseRoot '.local/logs/search.err.log')
    $owned = New-CourseProcessRecord 'search' $process
    ConvertTo-Json -InputObject @($records + $owned) | Set-Content -LiteralPath $recordPath -Encoding utf8
    $port = ($ports | Where-Object field -eq 'MetricsAddr').port
    $limit = [DateTime]::UtcNow.AddSeconds(40)
    do {
        Assert-CourseProcessesAlive @($owned)
        try {
            $response = Invoke-WebRequest "http://127.0.0.1:$port/readyz" -UseBasicParsing -TimeoutSec 3
            if ($response.StatusCode -eq 200) { Write-Host 'Search started; verify convergence with scripts/demo-search.ps1.'; return }
        } catch { }
        Start-Sleep -Milliseconds 250
    } while ([DateTime]::UtcNow -lt $limit)
    throw 'Search readiness timed out; inspect .local/logs/search.err.log.'
} catch {
    if ($owned) {
        Stop-CourseOwnedProcess $owned
        ConvertTo-Json -InputObject @($records) | Set-Content -LiteralPath $recordPath -Encoding utf8
    }
    throw
}
