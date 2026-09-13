#requires -Version 7.0
param(
    [ValidateSet('Status','Setup','Reset')][string]$Action = 'Setup',
    [ValidateSet('Docker','Local')][string]$Mode = 'Docker',
    [string]$Username = 'admin',
    [string]$DisplayName = '管理员',
    [PSCredential]$Credential
)
$ErrorActionPreference = 'Stop'

function Get-AccountAdminInvocation {
    param([string]$Mode,[string]$Root,[string]$Command,[string]$Username,[string]$DisplayName)
    $cliArgs = @($Command,'--tenant','demo_tenant')
    if ($Command -ne 'status') { $cliArgs += @('--username',$Username) }
    if ($Command -eq 'bootstrap') { $cliArgs += @('--display-name',$DisplayName) }
    if ($Mode -eq 'Docker') {
        return [pscustomobject]@{
            Path = (Get-Command docker -ErrorAction Stop).Source
            Arguments = @('compose','-p','meshops-demo','-f',(Join-Path $Root 'compose.demo.yml'),'run','--rm','-T','--no-deps','init','exec','/app/account-admin') + $cliArgs
        }
    }
    return [pscustomobject]@{Path=(Join-Path $Root 'bin/account-admin.exe');Arguments=$cliArgs}
}

function Invoke-AccountAdminCommand {
    param($Invocation,[string]$Payload='')
    $start = [Diagnostics.ProcessStartInfo]::new()
    $start.FileName = $Invocation.Path
    $start.UseShellExecute = $false
    $start.CreateNoWindow = $true
    $start.RedirectStandardInput = $true
    $start.RedirectStandardOutput = $true
    $start.RedirectStandardError = $true
    $start.StandardInputEncoding = [Text.UTF8Encoding]::new($false)
    $start.StandardOutputEncoding = [Text.UTF8Encoding]::new($false)
    $start.StandardErrorEncoding = [Text.UTF8Encoding]::new($false)
    foreach ($argument in $Invocation.Arguments) { $start.ArgumentList.Add([string]$argument) }
    $process = [Diagnostics.Process]::new()
    $process.StartInfo = $start
    try {
        if (-not $process.Start()) { throw 'Account management process failed to start.' }
        # 同时排空输出，避免子进程管道写满；密码只经过标准输入。
        $stdout = $process.StandardOutput.ReadToEndAsync()
        $stderr = $process.StandardError.ReadToEndAsync()
        if ($Payload) { $process.StandardInput.Write($Payload) }
        $process.StandardInput.Close()
        if (-not $process.WaitForExit(120000)) {
            $process.Kill($true)
            throw 'Account command timed out; inspect service availability before retrying.'
        }
        $raw = $stdout.GetAwaiter().GetResult()
        $null = $stderr.GetAwaiter().GetResult()
        if ($process.ExitCode -ne 0) {
            throw "Account command failed (exit $($process.ExitCode)). Check initialization, username and password requirements; existing accounts were not automatically replaced."
        }
        try { return $raw | ConvertFrom-Json -ErrorAction Stop }
        catch { throw 'Account command returned invalid JSON.' }
    } finally { $process.Dispose() }
}

function Invoke-AccountAdministration {
    param([string]$Action,[string]$Mode,[string]$Username,[string]$DisplayName,[PSCredential]$Credential)
    $root = Split-Path $PSScriptRoot -Parent
    if ($Mode -eq 'Local') { . (Join-Path $PSScriptRoot 'env.ps1') }
    if ($Action -in @('Status','Setup')) {
        $status = Invoke-AccountAdminCommand (Get-AccountAdminInvocation $Mode $root 'status' $Username $DisplayName)
        if ($Action -eq 'Status') { return $status }
        if ($status.initialized) {
            Write-Host "Administrator '$($status.username)' already exists; password unchanged."
            return
        }
    }
    $first = $null
    $second = $null
    $confirmation = $null
    $payload = $null
    try {
    if (-not $Credential) {
        $first = Read-Host '设置管理员密码（12—128 个字符）' -AsSecureString
        $second = Read-Host '再次输入管理员密码' -AsSecureString
        $Credential = [PSCredential]::new($Username,$first)
        $confirmation = [PSCredential]::new($Username,$second)
        if ($Credential.GetNetworkCredential().Password -cne $confirmation.GetNetworkCredential().Password) { throw '两次密码不一致，未执行修改。' }
    } elseif ($Credential.UserName -cne $Username) { throw 'Credential username must match -Username.' }
        $payload = @{password=$Credential.GetNetworkCredential().Password} | ConvertTo-Json -Compress
        $command = if ($Action -eq 'Setup') { 'bootstrap' } else { 'reset-admin' }
        $result = Invoke-AccountAdminCommand (Get-AccountAdminInvocation $Mode $root $command $Username $DisplayName) $payload
        if (-not $result.account) { throw 'Account command response is incomplete.' }
        Write-Host "管理员账号 '$Username' 已就绪。请使用你刚设置的密码登录；密码不会显示或写入文件。"
    } finally {
        $payload = $null
        $Credential = $null
        $confirmation = $null
        if ($first) { $first.Dispose() }
        if ($second) { $second.Dispose() }
    }
}

# 点加载只导入函数，供无依赖测试使用；正常执行才读取用户密码。
if ($MyInvocation.InvocationName -ne '.') {
    Invoke-AccountAdministration -Action $Action -Mode $Mode -Username $Username -DisplayName $DisplayName -Credential $Credential
}
