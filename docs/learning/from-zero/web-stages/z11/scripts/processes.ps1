# Shared Windows process ownership checks. Never identify a process by PID alone.
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
            # PowerShell 7 ConvertFrom-Json can turn ISO strings into DateTime.
            $expectedTicks = $Record.started.ToUniversalTime().Ticks
        } elseif ($Record.started -is [DateTimeOffset]) {
            $expectedTicks = $Record.started.UtcDateTime.Ticks
        } else {
            $expectedTicks = [DateTime]::Parse([string]$Record.started, [Globalization.CultureInfo]::InvariantCulture, [Globalization.DateTimeStyles]::RoundtripKind).ToUniversalTime().Ticks
        }
        if ($candidate.StartTime.ToUniversalTime().Ticks -ne $expectedTicks) { return $null }
        return $candidate
    } catch {
        # A vanished, inaccessible or malformed record is never permission to kill.
        return $null
    }
}

function New-CourseProcessRecord([string]$Name, [Diagnostics.Process]$Process) {
    # Start-Process can return before the executable module path is readable.
    # Do not persist a null path, which would make safe cleanup impossible.
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
        # Store as a string to preserve all 100ns ticks through any JSON reader.
        startedTicks = $Process.StartTime.ToUniversalTime().Ticks.ToString([Globalization.CultureInfo]::InvariantCulture)
    }
}

function Stop-CourseOwnedProcess($Record, [int]$TimeoutMilliseconds = 10000) {
    $owned = Get-CourseOwnedProcess $Record
    if (-not $owned) { return }
    # Stop-Process is abrupt on Windows; persistent restart semantics must hold.
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
            # These two scalar keys belong to the checked-in service YAML format.
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
