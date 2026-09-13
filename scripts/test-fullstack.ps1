<#
.SYNOPSIS
Run bounded, real HTTP acceptance against an already running local MeshOps demo.
.DESCRIPTION
Creates five simulated inspection tasks and verifies their persisted outcomes.
Does not start, stop or reset services. Passwords remain in memory; the JSON
evidence contains only selected non-secret fields. See test-fullstack.md.
#>
param(
    [string]$BaseURL = 'http://127.0.0.1:18090',
    [string]$OperatorUsername = $env:MESHOPS_WEB_OPERATOR_USERNAME,
    [string]$OperatorPassword = $env:MESHOPS_WEB_OPERATOR_PASSWORD,
    [string]$AdminUsername = $env:MESHOPS_WEB_ADMIN_USERNAME,
    [string]$AdminPassword = $env:MESHOPS_WEB_ADMIN_PASSWORD,
    [string]$OutputPath
)
$ErrorActionPreference = 'Stop'

# 测试凭证绝不能发送到外部指定的地址或重定向目标。
# 创建 HTTP 客户端前，先完整校验基础地址的主机和端口。
$base = $null
$address = $null
if (-not [Uri]::TryCreate($BaseURL,[UriKind]::Absolute,[ref]$base) -or
    $base.Scheme -notin @('http','https') -or $base.UserInfo -or
    $base.AbsolutePath -ne '/' -or $base.Query -or $base.Fragment -or
    (-not ($base.DnsSafeHost -ieq 'localhost' -or
       ([Net.IPAddress]::TryParse($base.DnsSafeHost,[ref]$address) -and [Net.IPAddress]::IsLoopback($address))))) {
    throw 'BaseURL must be a loopback HTTP(S) origin without credentials, path, query or fragment.'
}
if (-not $OperatorUsername -or -not $AdminUsername -or $OperatorUsername -ceq $AdminUsername -or -not $OperatorPassword -or -not $AdminPassword) {
    throw 'Provide distinct operator/admin usernames and their passwords for this environment.'
}
Add-Type -AssemblyName System.Net.Http
$origin = $base.GetLeftPart([UriPartial]::Authority)
$runId = [Guid]::NewGuid().ToString('N')
if (-not $OutputPath) { $OutputPath = Join-Path (Split-Path $PSScriptRoot -Parent) "results/fullstack-$runId.json" }
$OutputPath = [IO.Path]::GetFullPath($OutputPath)
if (Test-Path -LiteralPath $OutputPath) { throw 'OutputPath already exists; choose a new evidence file.' }
$started = [DateTimeOffset]::UtcNow
$globalDeadline = $started.AddMinutes(10)
$clients = [Collections.Generic.List[object]]::new()
$protectedValues = [Collections.Generic.List[string]]::new()
$protectedValues.Add($OperatorPassword)
$protectedValues.Add($AdminPassword)
$checks = [Collections.Generic.List[object]]::new()
$tasks = [Collections.Generic.List[object]]::new()
$snapshots = [Collections.Generic.List[object]]::new()
$phase = 'login'
$report = [ordered]@{
    schemaVersion = 1; runId = $runId; baseURL = $origin; startedAt = $started.ToString('o')
    passed = $false; checks = $checks; snapshots = $snapshots; tasks = $tasks
    scope = 'Real HTTP sessions, six snapshots, four inspect completions, cancellation, history, dispatch, CDC convergence and role boundaries. No Docker control or browser visual QA.'
}
function Fail-Fullstack([string]$Message) {
    $e = [Exception]::new($Message)
    $e.Data['safeFullstackMessage'] = $true
    throw $e
}
function Assert-Fullstack([bool]$Condition,[string]$Message) {
    if (-not $Condition) { Fail-Fullstack $Message }
}
function Add-Check([string]$Name,$Detail) {
    $checks.Add([ordered]@{name=$Name;passed=$true;checkedAt=[DateTimeOffset]::UtcNow.ToString('o');detail=$Detail})
    Write-Host "PASS: $Name"
}
function New-BrowserClient([string]$Role) {
    $handler = [Net.Http.HttpClientHandler]::new()
    $handler.AllowAutoRedirect = $false
    $handler.UseProxy = $false
    $handler.CookieContainer = [Net.CookieContainer]::new()
    $http = [Net.Http.HttpClient]::new($handler)
    $http.Timeout = [TimeSpan]::FromSeconds(10)
    $http.MaxResponseContentBufferSize = 2MB
    $actor = [pscustomobject]@{role=$Role;actorId='';http=$http;handler=$handler;csrf='';loggedIn=$false;clockOffset=[TimeSpan]::Zero}
    $clients.Add($actor)
    return $actor
}
function Invoke-Browser($Actor,[string]$Method,[string]$Path,$Body=$null,[int[]]$Expected=@(200)) {
    if ([DateTimeOffset]::UtcNow -ge $globalDeadline) { Fail-Fullstack 'Overall HTTP acceptance exceeded ten minutes.' }
    $request = [Net.Http.HttpRequestMessage]::new([Net.Http.HttpMethod]::new($Method),$origin+$Path)
    [void]$request.Headers.TryAddWithoutValidation('Origin',$origin)
    if ($Method -notin @('GET','HEAD') -and $Actor.csrf) { [void]$request.Headers.TryAddWithoutValidation('X-CSRF-Token',$Actor.csrf) }
    if ($null -ne $Body) {
        $json = ConvertTo-Json -InputObject $Body -Depth 12 -Compress
        $request.Content = [Net.Http.StringContent]::new($json,[Text.Encoding]::UTF8,'application/json')
    }
    $response = $null
    try {
        try { $response = $Actor.http.SendAsync($request).GetAwaiter().GetResult() }
        catch { Fail-Fullstack "HTTP transport failed during $phase. Check the local gateway and backend logs." }
        $status = [int]$response.StatusCode
        if ($status -notin $Expected) { Fail-Fullstack "Unexpected HTTP $status during $phase ($Method)." }
        $raw = $response.Content.ReadAsStringAsync().GetAwaiter().GetResult()
        $data = $null
        if ($raw) {
            try { $data = $raw | ConvertFrom-Json }
            catch { Fail-Fullstack "Invalid JSON response during $phase." }
        }
        return [pscustomobject]@{status=$status;data=$data}
    } finally {
        if ($response) { $response.Dispose() }
        $request.Dispose()
    }
}
function Wait-Fullstack([string]$Description,[scriptblock]$Read,[scriptblock]$Accept,[int]$Seconds=60) {
    $deadline = [DateTimeOffset]::UtcNow.AddSeconds($Seconds)
    do {
        $value = & $Read
        if (& $Accept $value) { return $value }
        Start-Sleep -Milliseconds 350
    } while ([DateTimeOffset]::UtcNow -lt $deadline -and [DateTimeOffset]::UtcNow -lt $globalDeadline)
    Fail-Fullstack "$Description did not converge within its bounded wait."
}
function Get-TaskFact($Actor,[string]$TaskID) {
    $result = Invoke-Browser $Actor 'GET' ('/api/v1/tasks/'+[Uri]::EscapeDataString($TaskID))
    Assert-Fullstack ($null -ne $result.data.task) 'GetTask returned no task.'
    return $result.data.task
}
function Wait-Task($Actor,[string]$TaskID,[string]$ExpectedStatus,[int]$Seconds=60) {
    return Wait-Fullstack "Task $ExpectedStatus" { Get-TaskFact $Actor $TaskID } {
        param($value)
        if ($value.status -in @('TASK_STATUS_SUCCEEDED','TASK_STATUS_FAILED','TASK_STATUS_CANCELLED','TASK_STATUS_TIMED_OUT','TASK_STATUS_REJECTED') -and $value.status -ne $ExpectedStatus) {
            Fail-Fullstack 'Task reached an unexpected terminal status.'
        }
        return $value.status -eq $ExpectedStatus
    } $Seconds
}
function Create-Inspection($Actor,[string]$EntityID,[int]$Duration,[string]$Keyword) {
    # 本次操作使用同一份完整请求体；HTTP 写入结果不确定时，
    # 不能自动更换业务幂等键后重新发送。
    $body = [ordered]@{idempotencyKey=[Guid]::NewGuid().ToString('D');taskType='inspect';targetEntityId=$EntityID;priority=5;deadline=([DateTimeOffset]::UtcNow+$Actor.clockOffset).AddMinutes(5).ToString('o');payload=@{payloadJson=(@{duration_seconds=$Duration;note=$Keyword}|ConvertTo-Json -Compress)}}
    $created = (Invoke-Browser $Actor 'POST' '/api/v1/tasks' $body).data
    Assert-Fullstack ([string]$created.taskId -match '^[a-z0-9_-]{1,128}$') 'CreateTask returned no valid task ID.'
    # 已确认成功的重复创建必须返回同一个业务任务。
    $again = (Invoke-Browser $Actor 'POST' '/api/v1/tasks' $body).data
    Assert-Fullstack ($again.taskId -ceq $created.taskId) 'Identical create request did not preserve task ID.'
    return [string]$created.taskId
}
function Read-TaskEvidence($Actor,$Fact) {
    Assert-Fullstack ($Fact.createdBy -ceq $Actor.actorId) 'Task creator differs from the authenticated personal account.'
    $path = '/api/v1/tasks/'+[Uri]::EscapeDataString([string]$Fact.taskId)
    $history = (Invoke-Browser $Actor 'GET' ($path+'/history')).data
    Assert-Fullstack (@($history.history).Count -gt 0) 'Task history is empty.'
    Assert-Fullstack (@($history.history | Where-Object toStatus -eq $Fact.status).Count -gt 0) 'Task terminal status is absent from history.'
    $dispatch = (Invoke-Browser $Actor 'GET' ($path+'/dispatch')).data
    Assert-Fullstack ($dispatch.taskId -ceq $Fact.taskId -and $dispatch.executionKey -ceq $Fact.executionKey -and $dispatch.dispatchId -and $dispatch.executorId -ceq $Fact.executorId) 'Dispatch identity differs from task execution identity.'
    return [ordered]@{taskId=$Fact.taskId;entityId=$Fact.targetEntityId;status=$Fact.status;statusVersion=$Fact.statusVersion;executionKey=$Fact.executionKey;executorId=$Fact.executorId;historyCount=@($history.history).Count;history=@($history.history | ForEach-Object {[ordered]@{from=$_.fromStatus;to=$_.toStatus;version=$_.statusVersion;at=$_.changedAt}});dispatchId=$dispatch.dispatchId;attempt=$dispatch.attempt;commandKind=$dispatch.commandKind;deliveryStatus=$dispatch.deliveryStatus}
}

try {
    $operator = New-BrowserClient 'operator'
    $admin = New-BrowserClient 'admin'
    foreach ($actor in @($operator,$admin)) {
        $password = if ($actor.role -eq 'operator') { $OperatorPassword } else { $AdminPassword }
        $username = if ($actor.role -eq 'operator') { $OperatorUsername } else { $AdminUsername }
        $login = (Invoke-Browser $actor 'POST' '/api/session' @{username=$username;password=$password}).data
        Assert-Fullstack (-not $login.mustChangePassword -and $login.username -ceq $username -and $login.actorId) 'Complete first password change before fullstack verification.'
        $actor.actorId = [string]$login.actorId
        Assert-Fullstack ($login.role -ceq $actor.role -and $login.csrfToken -and $login.tenantId -and $login.serverTime) 'Session response is missing trusted role, tenant, CSRF or clock.'
        $actor.csrf = [string]$login.csrfToken
        $actor.clockOffset = [DateTimeOffset]$login.serverTime - [DateTimeOffset]::UtcNow
        $actor.loggedIn = $true
        $protectedValues.Add($actor.csrf)
        foreach ($cookie in $actor.handler.CookieContainer.GetCookies([Uri]($origin+'/api/session'))) { $protectedValues.Add($cookie.Value) }
        $current = (Invoke-Browser $actor 'GET' '/api/session').data
        Assert-Fullstack ($current.actorId -ceq $login.actorId -and $current.tenantId -ceq $login.tenantId) 'Session identity changed between requests.'
    }
    Add-Check 'operator-and-admin-sessions' @{roles=@('operator','admin')}

    $phase = 'role-boundaries'
    $denied = Invoke-Browser $operator 'GET' '/api/v1/dispatcher/status' $null @(403)
    Assert-Fullstack ($denied.data.code -eq 7) 'Operator management rejection did not report PermissionDenied.'
    $runtime = (Invoke-Browser $admin 'GET' '/api/v1/dispatcher/status').data
    Assert-Fullstack ([bool]$runtime.asOf) 'Administrator status response has no sample time.'
    Add-Check 'operator-403-admin-200' @{operatorHTTP=403;adminHTTP=200;asOf=$runtime.asOf;consumerLag=[string]$(if($null -eq $runtime.consumerLag){'0'}else{$runtime.consumerLag});activeTasks=[int]$runtime.activeTasks;dlqCount=[string]$(if($null -eq $runtime.dlqCount){'0'}else{$runtime.dlqCount})}

    $phase = 'six-entity-snapshots'
    $inventory = (Invoke-Browser $operator 'GET' '/api/v1/entities').data
    $bindings = @{}
    foreach ($kind in @('person','drone','vehicle','robot','sensor','facility')) {
        $binding = @($inventory.entities | Where-Object entityType -eq $kind | Sort-Object entityId | Select-Object -First 1)
        Assert-Fullstack ($binding.Count -eq 1) "Inventory has no $kind entity."
        $binding = $binding[0]
        $bindings[$kind] = $binding
        $entityPath = '/api/v1/entities/'+[Uri]::EscapeDataString([string]$binding.entityId)
        $snapshot = Wait-Fullstack "Fresh $kind snapshot" { (Invoke-Browser $operator 'GET' $entityPath).data } {
            param($value)
            return $value.found -and $value.expiresAt -and [DateTimeOffset]$value.expiresAt -gt ([DateTimeOffset]::UtcNow+$operator.clockOffset).AddSeconds(2)
        } 60
        Assert-Fullstack ($snapshot.entityId -ceq $binding.entityId -and $snapshot.snapshot.entityType -ceq $kind) 'Snapshot identity/type mismatch.'
        Assert-Fullstack ([string]$snapshot.version -match '^[1-9][0-9]*$') 'Snapshot version is missing.'
        $snapshots.Add([ordered]@{entityId=$snapshot.entityId;entityType=$kind;version=[string]$snapshot.version;sourceId=$snapshot.sourceId;sourceGeneration=[string]$snapshot.sourceGeneration;expiresAt=$snapshot.expiresAt;viewGeneration=$snapshot.viewGeneration})
    }
    Add-Check 'six-real-snapshots' @{count=$snapshots.Count}

    foreach ($kind in @('person','drone','vehicle','robot')) {
        $phase = "inspect-$kind"
        $binding = $bindings[$kind]
        Assert-Fullstack ($binding.executorId -and 'inspect' -in @($binding.supportedTasks)) 'Registered entity has no inspection executor.'
        $entityPath = '/api/v1/entities/'+[Uri]::EscapeDataString([string]$binding.entityId)
        $ready = Wait-Fullstack "Executable $kind snapshot" { (Invoke-Browser $operator 'GET' $entityPath).data } {
            param($value)
            return $value.found -and $value.expiresAt -and [DateTimeOffset]$value.expiresAt -gt ([DateTimeOffset]::UtcNow+$operator.clockOffset).AddSeconds(2) -and $value.snapshot.status -notin @('offline','busy','fault') -and 'inspect' -in @($value.snapshot.capability.supportedTasks) -and (-not $value.snapshot.person -or $value.snapshot.person.onDuty)
        } 45
        $keyword = "httpproof$runId$kind"
        $taskID = Create-Inspection $operator $binding.entityId 1 $keyword
        $fact = Wait-Task $operator $taskID 'TASK_STATUS_SUCCEEDED'
        try { $result = $fact.resultJson | ConvertFrom-Json } catch { Fail-Fullstack 'Successful task result is not valid JSON.' }
        Assert-Fullstack ($result.effect_count -eq 1) 'Successful simulated inspection did not report effect_count=1.'
        $evidence = Read-TaskEvidence $operator $fact
        $evidence['effectCount'] = 1
        $evidence['keyword'] = $keyword
        $tasks.Add($evidence)
        Add-Check "inspect-$kind-succeeded" @{taskId=$taskID;effectCount=1}
    }

    $phase = 'cancel-running-task'
    $cancelID = Create-Inspection $operator $bindings['drone'].entityId 30 ("httpcancel$runId")
    $executing = Wait-Task $operator $cancelID 'TASK_STATUS_EXECUTING' 45
    $cancel = (Invoke-Browser $operator 'POST' ('/api/v1/tasks/'+$cancelID+'/cancel') @{reason='HTTP acceptance: cancel an executing simulation'}).data
    Assert-Fullstack ($cancel.cancelRequested) 'CancelTask did not acknowledge cancellation intent.'
    $cancelled = Wait-Task $operator $cancelID 'TASK_STATUS_CANCELLED' 45
    $cancelEvidence = Read-TaskEvidence $operator $cancelled
    $cancelEvidence['observedExecutingVersion'] = $executing.statusVersion
    $cancelEvidence['intentAccepted'] = $true
    $report['cancellation'] = $cancelEvidence
    Add-Check 'executing-cancellation-confirmed' @{taskId=$cancelID;status=$cancelled.status}

    $phase = 'cdc-search-convergence'
    foreach ($item in $tasks) {
        $searchPath = '/api/v1/search/tasks?keyword='+[Uri]::EscapeDataString($item.keyword)+'&target_entity_id='+[Uri]::EscapeDataString($item.entityId)+'&page_size=20'
        $matching = Wait-Fullstack 'CDC search terminal projection' {
            $page = (Invoke-Browser $operator 'GET' $searchPath).data
            return [pscustomobject]@{hits=@($page.tasks | Where-Object taskId -eq $item.taskId)}
        } {
            param($value)
            return $value.hits.Count -eq 1 -and $value.hits[0].status -ceq $item.status -and $value.hits[0].statusVersion -eq $item.statusVersion
        } 90
        $item['searchStatus'] = $matching.hits[0].status
        $item['searchVersion'] = $matching.hits[0].statusVersion
    }
    Add-Check 'four-task-cdc-convergence' @{count=$tasks.Count}
    $phase = 'logout'
    foreach ($actor in @($operator,$admin)) {
        $null = Invoke-Browser $actor 'DELETE' '/api/session' $null @(204)
        $actor.loggedIn = $false
        $null = Invoke-Browser $actor 'GET' '/api/session' $null @(401)
    }
    Add-Check 'both-sessions-revoked' @{roles=@('operator','admin')}
    $report.passed = $true
} catch {
    $reason = 'Acceptance stopped by a local validation or runtime error; inspect service logs without publishing credentials.'
    if ($_.Exception.Data['safeFullstackMessage']) { $reason = $_.Exception.Message }
    $report['failure'] = @{phase=$phase;reason=$reason}
} finally {
    foreach ($actor in $clients) {
        if ($actor.loggedIn) { try { $null = Invoke-Browser $actor 'DELETE' '/api/session' $null @(204) } catch { } }
        $actor.http.Dispose()
    }
    $report['finishedAt'] = [DateTimeOffset]::UtcNow.ToString('o')
    $report['elapsedSeconds'] = [Math]::Round(([DateTimeOffset]::UtcNow-$started).TotalSeconds,3)
    $serialized = ConvertTo-Json -InputObject $report -Depth 16
    foreach ($value in $protectedValues) {
        if ($value -and $serialized.Contains($value)) {
            $serialized = ConvertTo-Json -InputObject @{schemaVersion=1;runId=$runId;passed=$false;failure=@{phase='evidence-redaction';reason='Refused to save evidence containing a protected credential.'}} -Depth 4
            $report.passed = $false
            break
        }
    }
    [IO.Directory]::CreateDirectory((Split-Path $OutputPath -Parent)) | Out-Null
    [IO.File]::WriteAllText($OutputPath,$serialized,[Text.UTF8Encoding]::new($false))
}
if (-not $report.passed) { throw "HTTP acceptance failed during $phase. Sanitized evidence: $OutputPath" }
Write-Host "PASS: complete HTTP acceptance. Sanitized evidence: $OutputPath"
