# Z10 is now a frozen teaching milestone. Fullstack publication is separate.
param([switch]$Check)
$ErrorActionPreference = 'Stop'
$indexPath = Join-Path $PSScriptRoot 'checkpoint-index.json'
$index = Get-Content -LiteralPath $indexPath -Raw | ConvertFrom-Json
$stage = @($index.stages | Where-Object stage -eq 'z10')
if ($stage.Count -ne 1 -or $stage[0].source -ne 'business-stages/z10') { throw 'Z10 must reference the frozen pre-browser source.' }
foreach ($file in $stage[0].files) {
    $path = Join-Path $PSScriptRoot ($stage[0].source + '/' + $file.path)
    if ((Get-FileHash -LiteralPath $path -Algorithm SHA256).Hash.ToLowerInvariant() -cne $file.sha256) { throw "Frozen Z10 file differs: $($file.path)" }
}
'Frozen Z10 fingerprints verified. Publish new browser/deployment stages with publish-fullstack.py.'
