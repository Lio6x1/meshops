param(
    [ValidateSet('Up','Status','Logs','Codes','Stop','Down','Reset','RebuildSearch')]
    [string]$Action = 'Up',
    [ValidateSet('web','gateway','ingest','entity','task','dispatcher','search','canal','mysql','redis','kafka','elasticsearch','source-person','source-drone','source-vehicle','source-robot','source-sensor','source-facility','executor-person','executor-drone','executor-vehicle','executor-robot')]
    [string]$Service,
    [switch]$Follow,
    [switch]$NoBuild,
    [string]$BuildProxy = $env:MESHOPS_BUILD_PROXY,
    [switch]$ConfirmReset
)
$ErrorActionPreference = 'Stop'
$demoRoot = Split-Path $PSScriptRoot -Parent
$demoCompose = Join-Path $demoRoot 'compose.demo.yml'

function Invoke-DemoCompose([string[]]$Arguments) {
    # 固定项目名和绝对配置路径，使本演示栈与学习环境容器隔离，
    # 即使用户终端设置了 COMPOSE_PROJECT_NAME 也不会混用。
    & docker compose -p meshops-demo -f $demoCompose @Arguments
    if ($LASTEXITCODE -ne 0) { throw ('Demo Compose failed: ' + ($Arguments -join ' ') + '; inspect -Action Logs. Existing volumes were preserved.') }
}

if ($Action -eq 'Reset' -and -not $ConfirmReset) {
    throw 'Reset deletes only meshops-demo databases, queues and access codes. Explicitly pass -ConfirmReset to proceed.'
}
if ($Service -and $Action -ne 'Logs') { throw '-Service is only supported with -Action Logs.' }
if ($Follow -and $Action -ne 'Logs') { throw '-Follow is only supported with -Action Logs.' }
if ($BuildProxy -and $Action -eq 'Up' -and -not $NoBuild) {
    $proxyURI = $null
    if (-not [Uri]::TryCreate($BuildProxy,[UriKind]::Absolute,[ref]$proxyURI) -or $proxyURI.Scheme -notin @('http','https') -or $proxyURI.UserInfo) {
        throw '-BuildProxy must be an HTTP(S) proxy URL without embedded credentials.'
    }
}
if ($PSBoundParameters.ContainsKey('BuildProxy') -and ($Action -ne 'Up' -or $NoBuild)) { throw '-BuildProxy is only used by Up with building enabled.' }

$demoLock = $null
Push-Location $demoRoot
try {
    if ($Action -in @('Up','Stop','Down','Reset','RebuildSearch')) {
        New-Item -ItemType Directory -Force (Join-Path $demoRoot '.local') | Out-Null
        try { $demoLock = [IO.File]::Open((Join-Path $demoRoot '.local/demo-stack.lock'),[IO.FileMode]::OpenOrCreate,[IO.FileAccess]::ReadWrite,[IO.FileShare]::None) }
        catch { throw 'Another demo startup/stop/reset is active. Wait for it to finish.' }
    }
    switch ($Action) {
        'Up' {
            if (-not $NoBuild) {
                # 所有应用服务共享同一镜像，只通过 init 构建一次；
                # Canal 和前端使用各自独立的镜像构建目标。
                $buildArgs = @('build')
                if ($BuildProxy) { $buildArgs += @('--build-arg',"HTTPS_PROXY=$BuildProxy",'--build-arg',"HTTP_PROXY=$BuildProxy") }
                $buildArgs += @('init','canal','web')
                Invoke-DemoCompose $buildArgs
            }
            Invoke-DemoCompose @('run','--rm','--no-deps','init','secrets')
            Invoke-DemoCompose @('up','-d','--wait','--wait-timeout','240','mysql','redis','kafka','elasticsearch')
            # 维护启动可能执行迁移；迁移和初始化验证完成前，
            # 保持 Canal 与所有写入进程停止。
            Invoke-DemoCompose @('stop','web','gateway','search','canal','source-person','source-drone','source-vehicle','source-robot','source-sensor','source-facility','executor-person','executor-drone','executor-vehicle','executor-robot','task','dispatcher','entity','ingest')
            Invoke-DemoCompose @('run','--rm','--no-deps','init','initialize')
            Invoke-DemoCompose @('up','-d','--wait','--wait-timeout','240')
            # 容器健康检查在 Docker 内执行；还必须验证主机公开入口，
            # 因为内部路由正常时，主机入口仍可能单独故障。
            foreach ($probeURI in @('http://127.0.0.1:18090/healthz','http://127.0.0.1:18090/')) {
                try {
                    $probeResponse = Invoke-WebRequest -Uri $probeURI -UseBasicParsing -TimeoutSec 5
                    if ($probeResponse.StatusCode -ne 200) { throw "HTTP status $($probeResponse.StatusCode)" }
                } catch {
                    throw "Demo host HTTP probe failed at ${probeURI}: $($_.Exception.Message). Containers and volumes were preserved; inspect -Action Status and -Action Logs -Service web."
                }
            }
            Write-Host 'Demo is running: http://localhost:18090'
            Write-Host 'Use ./scripts/demo-stack.ps1 -Action Codes to read the two browser access codes.'
            Write-Host 'Container health and host HTTP checks passed; follow the end-to-end walkthrough to verify task execution and CDC search.'
        }
        'Status' { Invoke-DemoCompose @('ps','-a') }
        'Codes' { Invoke-DemoCompose @('run','--rm','--no-deps','init','codes') }
        'Logs' {
            $logArgs = @('logs','--tail','150')
            if ($Follow) { $logArgs += '--follow' }
            if ($Service) { $logArgs += $Service }
            Invoke-DemoCompose $logArgs
        }
        'Stop' { Invoke-DemoCompose @('stop') }
        'Down' { Invoke-DemoCompose @('down') }
        'RebuildSearch' {
            # 搜索属于派生数据，Task/Outbox 写入继续可用；
            # 快照实现只在建立一致性边界时短暂持有读锁。
            try {
                Invoke-DemoCompose @('stop','search','canal')
                Invoke-DemoCompose @('run','--rm','--no-deps','init','invalidate-search')
                Invoke-DemoCompose @('run','--rm','--no-deps','init','rebuild-search')
                # 新建容器也会丢弃旧容器可写层中的 TSDB。
                # 保留命名卷，只移除固定 meshops 目标目录中
                # 经过验证的游标和 H2 文件。
                Invoke-DemoCompose @('rm','-f','canal')
                Invoke-DemoCompose @('run','--rm','--no-deps','--entrypoint','/app/demo-init','canal','reset-canal-meta')
                Invoke-DemoCompose @('up','-d','--no-deps','--force-recreate','--wait','--wait-timeout','180','canal','search')
                Write-Host 'Search projection rebuilt. Create a new task and confirm CDC search convergence; other service data was preserved.'
            } catch {
                $rebuildFailure = $_
                # 不能基于不完整导入或旧 Canal 游标重新启动，
                # 导入成功后容器启动失败的情况也必须遵守此规则。
                try { Invoke-DemoCompose @('stop','search','canal') } catch { Write-Warning 'Could not confirm Search/Canal stopped; inspect Status before continuing.' }
                try { Invoke-DemoCompose @('run','--rm','--no-deps','init','invalidate-search') } catch { Write-Warning 'Could not invalidate bootstrap marker; keep Search stopped and inspect storage permissions.' }
                throw $rebuildFailure
            }
        }
        'Reset' {
            Invoke-DemoCompose @('down','--volumes','--remove-orphans')
            Write-Host 'Only the meshops-demo project volumes were removed. The next Up creates new data and access codes.'
        }
    }
} finally {
    if ($demoLock) { $demoLock.Dispose() }
    Pop-Location
}
