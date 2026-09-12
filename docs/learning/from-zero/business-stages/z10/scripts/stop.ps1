$ErrorActionPreference = 'Stop'
$courseRoot = Split-Path $PSScriptRoot -Parent
. (Join-Path $PSScriptRoot 'processes.ps1')
$recordPath = Join-Path $courseRoot '.local/processes.json'
if (-not (Test-Path -LiteralPath $recordPath)) { return }
$records = @(Get-Content -LiteralPath $recordPath -Raw | ConvertFrom-Json)
$remaining = @()
$failures = @()
foreach ($record in $records) {
    try { Stop-CourseOwnedProcess $record }
    catch { $remaining += $record; $failures += $_.Exception.Message }
}
ConvertTo-Json -InputObject @($remaining) | Set-Content -LiteralPath $recordPath -Encoding utf8
if ($failures.Count) { throw ($failures -join "`n") }
