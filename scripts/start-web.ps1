# 只启动 HTTP 网关；后端需另行启动，之后再运行 Vue 开发服务器。
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
. (Join-Path $PSScriptRoot 'processes.ps1')
$recordPath = Join-Path $root '.local/web-process.json'
if (Test-Path -LiteralPath $recordPath) {
    $old = Get-Content -LiteralPath $recordPath -Raw | ConvertFrom-Json
    if (Get-CourseOwnedProcess $old) { throw 'Web gateway is already running; use scripts/stop-web.ps1.' }
}
$probe = New-Object Net.Sockets.TcpListener ([Net.IPAddress]::Loopback),18090
try { $probe.Start() } finally { $probe.Stop() }
$binary = Join-Path $root 'bin/web-gateway.exe'
if (-not (Test-Path -LiteralPath $binary)) { throw 'Build the gateway first: go build -o bin/web-gateway.exe ./cmd/web-gateway' }
. (Join-Path $PSScriptRoot 'web-env.ps1')
$env:MESHOPS_SIMULATION_CONTROL = '1'
New-Item -ItemType Directory -Force (Join-Path $root '.local/logs') | Out-Null
$process = Start-Process -FilePath $binary -WorkingDirectory $root -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $root '.local/logs/web.out.log') -RedirectStandardError (Join-Path $root '.local/logs/web.err.log')
$record = New-CourseProcessRecord 'web-gateway' $process
$record | ConvertTo-Json | Set-Content -LiteralPath $recordPath -Encoding utf8
try {
    $deadline = [DateTime]::UtcNow.AddSeconds(20)
    $ready = $false
    do {
        Assert-CourseProcessesAlive @($record)
        try { $response = Invoke-WebRequest 'http://127.0.0.1:18090/livez' -TimeoutSec 2; $ready = $response.StatusCode -eq 200 } catch { }
        if ($ready) { break }
        Start-Sleep -Milliseconds 200
    } while ([DateTime]::UtcNow -lt $deadline)
    if (-not $ready) { throw 'Gateway startup failed; inspect .local/logs/web.err.log.' }
    'HTTP gateway ready. Start Vue with ./scripts/frontend.ps1 -Action Dev, then open http://localhost:5173.'
    'Browser access codes are in .local/web-secrets.json (never the backend RPC tokens).'
} catch { Stop-CourseOwnedProcess $record; throw }
