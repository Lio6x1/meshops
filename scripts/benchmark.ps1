param([ValidateRange(5,300)][int]$Seconds=30, [ValidateSet("mixed","person")][string]$Profile="mixed")
$ErrorActionPreference='Stop'
. (Join-Path $PSScriptRoot 'env.ps1')
Push-Location $courseRoot
try {
    & (Join-Path $PSScriptRoot 'build.ps1')
    & ./bin/verify.exe --mode benchmark --seconds $Seconds --profile $Profile
    if ($LASTEXITCODE -ne 0) { throw 'Benchmark incomplete; inspect the generated run logs under .local/verification.' }
} finally { Pop-Location }
