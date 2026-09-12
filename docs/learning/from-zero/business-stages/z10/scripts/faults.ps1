# 仅停止和重启专用 meshops-course 依赖。先停止演示，再串行执行；
# 不得与测试或压测同时运行。
$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'env.ps1')
Push-Location $courseRoot
try {
    # 恢复时必须重启原有实例。若只用基础 Compose 文件重建，
    # 可能悄悄丢失覆盖配置提供的设置，
    # 例如搜索链路使用的 MySQL binlog 选项。
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
