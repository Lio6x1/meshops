$ErrorActionPreference = 'Stop'
$scriptPath = Join-Path $PSScriptRoot 'test-fullstack.ps1'
if (-not (Test-Path -LiteralPath $scriptPath)) { throw 'test-fullstack.ps1 is missing' }
# Every rejected URL must fail before it can make an HTTP request or write evidence.
$invalid = @('http://example.com:18090','http://127.0.0.1:18090/api','http://user:pass@127.0.0.1:18090','http://127.0.0.1:18090?x=1','file:///C:/Windows','http://192.0.2.1:18090')
foreach ($url in $invalid) {
    $rejected = $false
    try { & $scriptPath -BaseURL $url -OperatorCode ('o' * 32) -AdminCode ('a' * 32) }
    catch { $rejected = $_.Exception.Message -like '*BaseURL*loopback*' }
    if (-not $rejected) { throw 'An invalid BaseURL did not fail local preflight.' }
}
'PASS: six invalid BaseURL forms rejected before network use.'
