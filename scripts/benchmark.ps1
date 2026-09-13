param([ValidateRange(5,300)][int]$Seconds=30, [ValidateSet("mixed","person")][string]$Profile="mixed", [ValidateRange(10,1000000)][int]$Entities=10000, [string]$Rates="100,500", [string]$Timeout="15m", [string]$WarmupTimeout="10m")
$ErrorActionPreference='Stop'
. (Join-Path $PSScriptRoot 'env.ps1')
Push-Location $courseRoot
try {
    & (Join-Path $PSScriptRoot 'build.ps1')
    & ./bin/verify.exe --mode benchmark --seconds $Seconds --profile $Profile --entities $Entities --rates $Rates --timeout $Timeout --warmup-timeout $WarmupTimeout
    if ($LASTEXITCODE -ne 0) { throw 'Benchmark incomplete; inspect the generated run logs under .local/verification.' }
} finally { Pop-Location }
