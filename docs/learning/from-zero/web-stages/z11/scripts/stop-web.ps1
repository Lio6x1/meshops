$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
. (Join-Path $PSScriptRoot 'processes.ps1')
$recordPath = Join-Path $root '.local/web-process.json'
if (Test-Path -LiteralPath $recordPath) {
    $record = Get-Content -LiteralPath $recordPath -Raw | ConvertFrom-Json
    Stop-CourseOwnedProcess $record
    Remove-Item -LiteralPath $recordPath
}
