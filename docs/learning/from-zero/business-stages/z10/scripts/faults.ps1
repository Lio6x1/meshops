# Stops/restarts only the dedicated meshops-course dependencies. Run serially,
# after stopping the demo; never concurrently with tests or the benchmark.
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'env.ps1')
Push-Location $courseRoot
try {
    # Recovery must restart these existing instances. Recreating from only the
    # base Compose file can silently remove settings supplied by an override
    # (for example Search's MySQL binlog options).
    $before = @{}
    foreach ($dependency in @('kafka','redis','mysql')) {
        $ids = @(& docker compose -f docker-compose.yml ps -q $dependency)
        if ($LASTEXITCODE -ne 0 -or $ids.Count -ne 1) { throw "Start the dedicated $dependency dependency before fault verification." }
        $before[$dependency] = $ids[0].Trim()
    }
    & (Join-Path $PSScriptRoot 'build.ps1')
    & ./bin/verify.exe --mode faults
    if ($LASTEXITCODE -ne 0) { throw 'dependency fault verification failed; inspect the reported evidence directory' }
    foreach ($dependency in @('kafka','redis','mysql')) {
        $ids = @(& docker compose -f docker-compose.yml ps -q $dependency)
        if ($LASTEXITCODE -ne 0 -or $ids.Count -ne 1 -or $ids[0].Trim() -cne $before[$dependency]) {
            throw "$dependency container changed; fault recovery must preserve existing container configuration."
        }
    }
} finally { Pop-Location }
