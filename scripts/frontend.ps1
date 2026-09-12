param([ValidateSet('Install','Dev','Build','Test')][string]$Action = 'Dev')
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
$node = (Get-Command node -ErrorAction Stop).Source
# 使用所选 Node 安装附带的 npm，避免用户目录中遗留的
# npm.ps1 包装脚本仍指向已删除的 npm 安装。
$npmCLI = Join-Path (Split-Path $node -Parent) 'node_modules/npm/bin/npm-cli.js'
if (-not (Test-Path -LiteralPath $npmCLI)) { throw 'This Node installation has no bundled npm; install Node with npm.' }
Push-Location (Join-Path $root 'web')
try {
    switch ($Action) {
        Install { & $node $npmCLI ci }
        Dev { & $node $npmCLI run dev }
        Build { & $node $npmCLI run build }
        Test { & $node $npmCLI test }
    }
    if ($LASTEXITCODE -ne 0) { throw "Frontend $Action failed (exit $LASTEXITCODE)." }
} finally { Pop-Location }
