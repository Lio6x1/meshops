# 每个新终端都需点加载；凭证保存在被 Git 忽略的 .local 存储中。
param([switch]$AllowIncomplete)
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'env.ps1')
$searchSecretPath = Join-Path $courseRoot '.local/search-secrets.json'
if (-not (Test-Path -LiteralPath $searchSecretPath)) {
    # Canal 在 COM_REGISTER_SLAVE 中发送此凭证；MySQL 8.0 的
    # report-password 字段最多容纳 32 字符，24 个随机字节提供 192 位熵。
    $bytes = New-Object byte[] 24
    $rng = [Security.Cryptography.RandomNumberGenerator]::Create()
    try { $rng.GetBytes($bytes) } finally { $rng.Dispose() }
    @{ MESHOPS_CANAL_PASSWORD = [Convert]::ToBase64String($bytes) } | ConvertTo-Json | Set-Content -LiteralPath $searchSecretPath -Encoding utf8
}
$searchSecrets = Get-Content -LiteralPath $searchSecretPath -Raw | ConvertFrom-Json
$env:MESHOPS_CANAL_PASSWORD = [string]$searchSecrets.MESHOPS_CANAL_PASSWORD
if ($env:MESHOPS_CANAL_PASSWORD -notmatch '^[A-Za-z0-9+/]{32}$') { throw 'Canal requires a 32-character random credential; inspect .local/search-secrets.json.' }
$env:MESHOPS_ES_ENDPOINT = 'http://127.0.0.1:19200'
$env:MESHOPS_SEARCH_ENDPOINT = '127.0.0.1:50055'
$searchMarkerPath = Join-Path $courseRoot '.local/search-bootstrap.json'
$env:MESHOPS_CANAL_BINLOG_FILE = 'NOT_BOOTSTRAPPED'
$env:MESHOPS_CANAL_BINLOG_POSITION = '4'
if (Test-Path -LiteralPath $searchMarkerPath) {
    $searchMarker = Get-Content -LiteralPath $searchMarkerPath -Raw | ConvertFrom-Json
    if (-not $searchMarker.complete) {
        if (-not $AllowIncomplete) { throw 'Search bootstrap is incomplete; run scripts/rebuild-search.ps1.' }
    } else {
        $env:MESHOPS_CANAL_BINLOG_FILE = [string]$searchMarker.position.file
        $env:MESHOPS_CANAL_BINLOG_POSITION = [string]$searchMarker.position.offset
    }
}
