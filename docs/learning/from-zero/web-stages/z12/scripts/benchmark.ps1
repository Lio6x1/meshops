param([int]$Seconds=30)
$ErrorActionPreference='Stop'
. (Join-Path $PSScriptRoot 'env.ps1')
Push-Location $courseRoot
try {
    & (Join-Path $PSScriptRoot 'build.ps1')
    & ./bin/verify.exe --mode benchmark --seconds $Seconds
    if ($LASTEXITCODE -ne 0) { throw 'Benchmark incomplete; inspect the generated run logs under .local/verification.' }
} finally { Pop-Location }
