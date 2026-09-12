$ErrorActionPreference = 'Stop'
$scriptPath = Join-Path $PSScriptRoot 'test-fullstack.ps1'
if (-not (Test-Path -LiteralPath $scriptPath)) { throw 'test-fullstack.ps1 is missing' }
# 所有不合规 URL 都必须在发送 HTTP 请求或写入证据前被拒绝。
$invalid = @('http://example.com:18090','http://127.0.0.1:18090/api','http://user:pass@127.0.0.1:18090','http://127.0.0.1:18090?x=1','file:///C:/Windows','http://192.0.2.1:18090')
foreach ($url in $invalid) {
    $rejected = $false
    try { & $scriptPath -BaseURL $url -OperatorCode ('o' * 32) -AdminCode ('a' * 32) }
    catch { $rejected = $_.Exception.Message -like '*BaseURL*loopback*' }
    if (-not $rejected) { throw 'An invalid BaseURL did not fail local preflight.' }
}
'PASS: six invalid BaseURL forms rejected before network use.'
