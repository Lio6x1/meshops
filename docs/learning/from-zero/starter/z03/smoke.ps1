$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot
New-Item -ItemType Directory -Force .local | Out-Null
foreach ($tool in @('entity', 'query', 'put')) {
    go build -o ".local/$tool.exe" "./cmd/$tool"
    if ($LASTEXITCODE -ne 0) { throw "build $tool failed" }
}
# Let the OS choose a free port. A different process could still claim it
# between Stop and server start; rerun the script if that rare race occurs.
$probe = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 0)
$probe.Start()
$port = $probe.LocalEndpoint.Port
$probe.Stop()
$addr = "127.0.0.1:$port"
$testConfig = Join-Path $PSScriptRoot '.local/smoke.yaml'
(Get-Content -LiteralPath 'configs/entity.yaml' -Raw) -replace '127.0.0.1:\d+', $addr |
    Set-Content -LiteralPath $testConfig -Encoding utf8
$entityExe = Join-Path $PSScriptRoot '.local/entity.exe'
$queryExe = Join-Path $PSScriptRoot '.local/query.exe'
function Start-Entity {
    $p = Start-Process -FilePath $entityExe -ArgumentList @('-f', ('"' + $testConfig + '"')) -WindowStyle Hidden -PassThru -RedirectStandardOutput '.local/entity.out.log' -RedirectStandardError '.local/entity.err.log'
    for ($i = 0; $i -lt 100; $i++) {
        if ($p.HasExited) { throw 'entity exited; inspect .local/entity.err.log' }
        $null = & $queryExe -addr $addr -id smoke-probe 2>&1
        if ($LASTEXITCODE -eq 0) { return $p }
        Start-Sleep -Milliseconds 100
    }
    Stop-Process -Id $p.Id -ErrorAction SilentlyContinue
    throw 'entity did not become ready'
}
function Expect-Query([string]$id, [string]$expected) {
    $actual = & $queryExe -addr $addr -id $id
    if ($LASTEXITCODE -ne 0 -or $actual -ne $expected) { throw "query mismatch: $actual" }
    Write-Output $actual
}
$server = $null
try {
    $server = Start-Entity
    $putExe = Join-Path $PSScriptRoot '.local/put.exe'
    Expect-Query 'person-001' 'id=person-001 found=false'
    & $putExe -addr $addr -id person-001 -type person
    if ($LASTEXITCODE -ne 0) { throw 'person write failed' }
    Expect-Query 'person-001' 'id=person-001 found=true type=person lat=31.2304 lon=121.4737 power=unknown'
    & $putExe -addr $addr -id drone-001 -type drone -battery 0
    if ($LASTEXITCODE -ne 0) { throw 'drone write failed' }
    Expect-Query 'drone-001' 'id=drone-001 found=true type=drone lat=31.2304 lon=121.4737 power=0.0%'
    & $putExe -addr $addr -id drone-001 -type drone -lat 32 -battery 80
    if ($LASTEXITCODE -ne 0) { throw 'update failed' }
    Expect-Query 'drone-001' 'id=drone-001 found=true type=drone lat=32.0000 lon=121.4737 power=80.0%'
    $invalid = & $putExe -addr $addr -id drone-001 -type drone -lat 91 2>&1
    if ($LASTEXITCODE -eq 0 -or "$invalid" -notmatch 'InvalidArgument') { throw "invalid request was not rejected: $invalid" }
    Write-Output 'invalid latitude rejected: InvalidArgument'
    Expect-Query 'drone-001' 'id=drone-001 found=true type=drone lat=32.0000 lon=121.4737 power=80.0%'
    Stop-Process -Id $server.Id
    $server.WaitForExit()
    $server = Start-Entity
    Expect-Query 'person-001' 'id=person-001 found=false'
    Expect-Query 'drone-001' 'id=drone-001 found=false'
    Write-Output 'PASS: z03 real gRPC write/query/update/rejection/restart loss'
} finally {
    if ($null -ne $server -and !$server.HasExited) {
        Stop-Process -Id $server.Id
        $server.WaitForExit()
    }
}
