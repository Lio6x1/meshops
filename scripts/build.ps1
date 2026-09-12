$ErrorActionPreference = 'Stop'
$courseRoot = Split-Path $PSScriptRoot -Parent
Push-Location $courseRoot
try {
    New-Item -ItemType Directory -Force -Path bin | Out-Null
    foreach ($name in @('ingest','entity','task','dispatcher','opctl','gateway-simulator','executor-simulator','verify','search','search-admin')) {
        & go build -o "bin/$name.exe" "./cmd/$name"
        if ($LASTEXITCODE -ne 0) { throw "build failed: $name" }
    }
} finally { Pop-Location }
