# 共享的 Windows 进程归属检查；不能只凭 PID 识别进程。
function Get-CourseOwnedProcess($Record) {
    try {
        if (-not $Record.pid -or -not $Record.path) { return $null }
        $candidate = Get-Process -Id ([int]$Record.pid) -ErrorAction SilentlyContinue
        if (-not $candidate -or $candidate.HasExited) { return $null }
        $expectedPath = [IO.Path]::GetFullPath([string]$Record.path)
        if (-not [string]::Equals($candidate.Path, $expectedPath, [StringComparison]::OrdinalIgnoreCase)) { return $null }
        if ($Record.startedTicks) {
            $expectedTicks = [Int64]::Parse([string]$Record.startedTicks, [Globalization.CultureInfo]::InvariantCulture)
        } elseif ($Record.started -is [DateTime]) {
            # PowerShell 7 的 ConvertFrom-Json 可能将 ISO 字符串转换为 DateTime。
            $expectedTicks = $Record.started.ToUniversalTime().Ticks
        } elseif ($Record.started -is [DateTimeOffset]) {
            $expectedTicks = $Record.started.UtcDateTime.Ticks
        } else {
            $expectedTicks = [DateTime]::Parse([string]$Record.started, [Globalization.CultureInfo]::InvariantCulture, [Globalization.DateTimeStyles]::RoundtripKind).ToUniversalTime().Ticks
        }
        if ($candidate.StartTime.ToUniversalTime().Ticks -ne $expectedTicks) { return $null }
        return $candidate
    } catch {
        # 记录消失、无法访问或格式错误，都不构成终止进程的授权。
        return $null
    }
}

function New-CourseProcessRecord([string]$Name, [Diagnostics.Process]$Process) {
    # Start-Process 返回时，可执行模块路径可能还无法读取。
    # 不能持久化空路径，否则后续无法安全确认进程归属并清理。
    $identityDeadline = [DateTime]::UtcNow.AddSeconds(3)
    $expectedTicks = $Process.StartTime.ToUniversalTime().Ticks
    do {
        $fresh = Get-Process -Id $Process.Id -ErrorAction SilentlyContinue
        if (-not $fresh -or $fresh.HasExited) { throw "Process $Name exited before its identity was recorded." }
        if ($fresh.StartTime.ToUniversalTime().Ticks -ne $expectedTicks) { throw "Process $Name changed identity during startup." }
        if ($fresh.Path) { $Process = $fresh; break }
        Start-Sleep -Milliseconds 20
    } while ([DateTime]::UtcNow -lt $identityDeadline)
    if (-not $Process.Path) { throw "Cannot record the executable path for $Name." }
    return [PSCustomObject]@{
        name = $Name
        pid = $Process.Id
        path = $Process.Path
        started = $Process.StartTime.ToUniversalTime().ToString('o')
        # 按字符串保存，避免 JSON 读取器丢失 100ns 刻度的精度。
        startedTicks = $Process.StartTime.ToUniversalTime().Ticks.ToString([Globalization.CultureInfo]::InvariantCulture)
    }
}

function Stop-CourseOwnedProcess($Record, [int]$TimeoutMilliseconds = 10000) {
    $owned = Get-CourseOwnedProcess $Record
    if (-not $owned) { return }
    # Windows 的 Stop-Process 会直接终止进程，持久化重启语义仍必须成立。
    try { Stop-Process -InputObject $owned -Force -ErrorAction Stop }
    catch { if (-not $owned.HasExited) { throw } }
    if (-not $owned.WaitForExit($TimeoutMilliseconds)) {
        throw "Owned process $($Record.name) (PID $($Record.pid)) did not exit within $TimeoutMilliseconds ms."
    }
}

function Assert-CourseProcessesAlive([object[]]$Records) {
    foreach ($record in $Records) {
        if (-not (Get-CourseOwnedProcess $record)) {
            throw "Owned process $($record.name) (PID $($record.pid)) exited or changed identity; see .local/logs."
        }
    }
}

function Get-CourseServicePorts([string]$Root, [string[]]$Roles = @('entity', 'task', 'dispatcher', 'ingest')) {
    foreach ($role in $Roles) {
        $configPath = Join-Path $Root "configs/$role.yaml"
        $config = Get-Content -LiteralPath $configPath -Raw
        foreach ($field in @('ListenOn', 'MetricsAddr')) {
            # 这两个标量键对应仓库中固定的服务 YAML 格式。
            $matches = [regex]::Matches($config, ('(?m)^\s*' + $field + ':\s*["'']?(?:\[[^\]]+\]|[^\s:"'']+):(\d+)["'']?\s*(?:#.*)?$'))
            if ($matches.Count -ne 1) { throw "Expected one explicit $field address in $configPath." }
            $port = [int]$matches[0].Groups[1].Value
            if ($port -lt 1 -or $port -gt 65535) { throw "Invalid $field port in $configPath." }
            [PSCustomObject]@{ role = $role; field = $field; port = $port }
        }
    }
}

function Assert-CoursePortsAvailable([object[]]$ServicePorts) {
    $duplicates = @($ServicePorts | Group-Object port | Where-Object Count -gt 1)
    if ($duplicates.Count) { throw "Course configuration reuses ports: $($duplicates.Name -join ', ')." }
    $listeningPorts = @([Net.NetworkInformation.IPGlobalProperties]::GetIPGlobalProperties().GetActiveTcpListeners() | ForEach-Object { $_.Port })
    $occupied = @($ServicePorts | Where-Object { $_.port -in $listeningPorts })
    if ($occupied.Count) {
        $details = @($occupied | ForEach-Object { "$($_.role)/$($_.field):$($_.port)" }) -join ', '
        throw "Course ports already occupied: $details. No processes were started or stopped."
    }
}
