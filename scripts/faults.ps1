# Stops/restarts only the dedicated meshops-course dependencies. Run serially,
# after stopping the demo; never concurrently with tests or the benchmark.
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'env.ps1')
Push-Location $courseRoot
try {
    & (Join-Path $PSScriptRoot 'build.ps1')
    & ./bin/verify.exe --mode faults
    if ($LASTEXITCODE -ne 0) { throw 'dependency fault verification failed; inspect the reported evidence directory' }
} finally { Pop-Location }
