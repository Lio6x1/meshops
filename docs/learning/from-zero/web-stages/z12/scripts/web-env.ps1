# Dot-source after backend environment setup. Browser codes are independent of RPC tokens.
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'env.ps1')
$webSecretsPath = Join-Path $courseRoot '.local/web-secrets.json'
if (-not (Test-Path -LiteralPath $webSecretsPath)) {
    $codes = @{}
    foreach ($name in @('MESHOPS_WEB_OPERATOR_CODE','MESHOPS_WEB_ADMIN_CODE')) {
        $bytes = New-Object byte[] 32
        $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
        try { $rng.GetBytes($bytes) } finally { $rng.Dispose() }
        $codes[$name] = [Convert]::ToBase64String($bytes)
    }
    $codes | ConvertTo-Json | Set-Content -LiteralPath $webSecretsPath -Encoding utf8
}
$codes = Get-Content -LiteralPath $webSecretsPath -Raw | ConvertFrom-Json
foreach ($name in @('MESHOPS_WEB_OPERATOR_CODE','MESHOPS_WEB_ADMIN_CODE')) {
    $value = [string]$codes.$name
    if ($value.Length -lt 32) { throw 'Invalid browser access code; inspect .local/web-secrets.json locally.' }
    [Environment]::SetEnvironmentVariable($name,$value,'Process')
}
$env:MESHOPS_MANIFEST = Join-Path $courseRoot 'configs/simulation.yaml'
$env:MESHOPS_WEB_TENANT = 'demo_tenant'
$env:MESHOPS_WEB_LISTEN = '127.0.0.1:18090'
$env:MESHOPS_SEARCH_ENDPOINT = '127.0.0.1:50055'
$env:MESHOPS_WEB_ORIGINS = 'http://localhost:18090,http://127.0.0.1:18090,http://localhost:5173,http://127.0.0.1:5173'
