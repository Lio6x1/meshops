$ErrorActionPreference = 'Stop'
$root = $PSScriptRoot
$index = Get-Content (Join-Path $root 'checkpoint-index.json') -Raw | ConvertFrom-Json
$output = Join-Path $root 'lessons/steps'
[IO.Directory]::CreateDirectory($output) | Out-Null
$utf8 = [Text.UTF8Encoding]::new($false)
$steps = [Collections.Generic.List[object]]::new()
# Only the first three tests depend on domain.go alone. Keep their bodies intact.
$test = [IO.File]::ReadAllText((Join-Path $root 'business-stages/z07/internal/tasks/domain_test.go')).Replace("`r`n", "`n")
$cut = $test.IndexOf('func TestTaskCursorBoundToTenantFilterAndPosition(')
if ($cut -lt 0) { throw 'Domain test boundary changed' }
$test = $test.Substring(0,$cut).Replace("`t`"os`"`n",'')
$override = 'substeps/z07-01/internal/tasks/domain_test.go'
[IO.Directory]::CreateDirectory((Split-Path (Join-Path $root $override))) | Out-Null
[IO.File]::WriteAllText((Join-Path $root $override), $test, $utf8)
foreach ($stageID in @('z04','z05','z06','z07','z08','z09','z10','z11','z12','z13')) {
    $stage = $index.stages | Where-Object stage -eq $stageID
    $pending = [Collections.Generic.List[string]]::new()
    foreach ($path in @($stage.changes.added)+@($stage.changes.replaced)) { $pending.Add($path) }
    if ($stageID -eq 'z04') {
        $definitions = @(
            @{id='z04-01'; title='协议升级与生成包'; paths=@($pending | Where-Object {$_ -match '^(proto/|gen/|go\.(mod|sum)$|scripts/generate.ps1$)'}); remove=@($stage.changes.removed); packages=@('./gen/...'); tests=@()},
            @{id='z04-02'; title='可信映射与两个适配器'; paths=@('internal/platform/types.go','internal/platform/config.go','internal/platform/registry.go','internal/state/adapters.go','internal/state/validation.go','internal/state/adapters_test.go','configs/server.yaml','configs/simulation.yaml','testdata/sources/person.json','testdata/sources/drone.json'); remove=@(); packages=@('./internal/state'); tests=@('TestTwoAdapters','TestStrictPerson')},
            @{id='z04-03'; title='身份与连续批次确认'; paths=@('internal/platform/auth.go','internal/state/ingest.go','internal/state/ingest_test.go'); remove=@(); packages=@('./internal/state'); tests=@('TestWholeBatchAndPrefixACK','TestResumeAndEpoch','TestSingleSourceStreamAndPersistentRateBudget')},
            @{id='z04-04'; title='组装内存查询和真实RPC'; paths=@('*'); remove=@(); packages=@('./internal/state'); tests=@('TestAuthenticatedWriteQueryAndVolatileRestart','TestMemoryVersionAndTenantRules')}
        )
    } elseif ($stageID -eq 'z05') {
        $definitions = @(
            @{id='z05-01'; title='Kafka客户端与有界超时'; paths=@('internal/bus/kafka.go','internal/bus/lag.go','internal/bus/retention.go','internal/bus/kafka_test.go'); remove=@(); packages=@('./internal/bus'); tests=@('TestOperationsHonorContextWithSilentBroker')},
            @{id='z05-02'; title='Redis投影、删除标记与分进程装配'; paths=@('*'); remove=@($stage.changes.removed); packages=@('./internal/state'); tests=@('TestRedisAtomicOrderingAndTombstone','TestKafkaACKThenRedisProjection'); needs=@('Redis','Kafka')}
        )
    } elseif ($stageID -eq 'z06') {
        $definitions = @(
            @{id='z06-01'; title='六类原始格式与组件校验'; paths=@('internal/state/adapters.go','internal/state/adapters_test.go','testdata/sources/facility.json','testdata/sources/robot.json','testdata/sources/sensor.json','testdata/sources/vehicle.json'); remove=@(); packages=@('./internal/state'); tests=@('TestSixAdapters','TestDroneCoordinatesAndFacilityCapacityAreValidated')},
            @{id='z06-02'; title='本地持久队列与版本事务'; paths=@('internal/edge/queue.go','internal/edge/queue_test.go'); remove=@(); packages=@('./internal/edge'); tests=@('TestQueueRestartAndActualSentACK','TestQueueCapacityAndGenerationRollback','TestQueueDuplicateDoesNotAllocateVersion','TestQueueCorruptionPreservesFile','TestCompactionRetainsOriginalAndAllState')},
            @{id='z06-03'; title='流式补传与ACK校验'; paths=@('internal/edge/uplink.go','internal/edge/uplink_test.go'); remove=@(); packages=@('./internal/edge'); tests=@('TestUploadACKLossAndCloseSendFinalACK','TestUploadPartialACKReplaysOnlySuffix','TestUploadRejectsInvalidACKWithoutDeleting','TestUploadStreamIsNotGivenUnaryFiveSecondDeadline','TestUploadCancellationUnblocksReceive','TestBackoffIsBounded')},
            @{id='z06-04'; title='订阅、模拟网关与完整状态链路'; paths=@('*'); remove=@(); packages=@('./internal/state'); tests=@('TestKafkaACKThenRedisProjection'); needs=@('Redis','Kafka')}
        )
    } elseif ($stageID -eq 'z08') {
        $definitions = @(
            # Constructor changes and same-package tests must move together: go test
            # compiles every test file even when -run selects only the sampling test.
            @{id='z08-01'; title='历史消费者与采样规则'; paths=@('internal/state/history.go','internal/state/entity.go','internal/state/state_test.go','internal/state/review_test.go','internal/app/run.go'); remove=@(); packages=@('./internal/state'); tests=@('TestSamplingPriorityAndCircularHeading')},
            @{id='z08-02'; title='历史查询、去重分页与清理'; paths=@('*'); remove=@(); packages=@('./internal/state'); tests=@('TestMySQLSampleIdempotencePagingAgeAndBudget','TestHistoryRetentionCatchesUpMoreThanOneBatch'); needs=@('MySQL')}
        )
    } elseif ($stageID -eq 'z09') {
        $definitions = @(
            # Rebuild defines PartitionBounds; recovery supplies atomic activation
            # and verified floors; Entity.Run must start using those floors now.
            @{id='z09-01'; title='影子generation与独立预期校验'; paths=@('internal/state/rebuild.go','internal/state/recovery.go','internal/state/entity.go','internal/state/state_test.go','internal/state/review_test.go','internal/state/rebuild_integration_test.go'); remove=@(); packages=@('./internal/state'); tests=@('TestRedisOrderingTombstoneAndManifest','TestVerifiedReplayFloorsRequireActivationEvidence'); needs=@('Redis')},
            @{id='z09-02'; title='故障恢复与压测报告工具'; paths=@($pending | Where-Object {$_ -match '^(internal/verification/|cmd/verify/)'}); remove=@(); packages=@('./internal/verification','./cmd/verify'); tests=@('TestStopUncertaintyStillRestoresDependency','TestRestoreErrorsAreNotLost','TestAllSupportedBenchmarkDurationsLeaveDrainBudget','TestReportWriteFailureReturnsNonzero')},
            @{id='z09-03'; title='维护入口、运行脚本与验收证据'; paths=@('*'); remove=@(); packages=@('./internal/verification','./cmd/verify'); tests=@('TestRestoreErrorsAreNotLost','TestReportWriteFailureReturnsNonzero')}
        )
    } elseif ($stageID -eq 'z10') {
        $definitions = @(
            @{id='z10-01'; title='先定义搜索协议'; paths=@($pending | Where-Object {$_ -match '^(proto/search/|gen/search/)'}); remove=@(); packages=@('./gen/...'); tests=@()},
            @{id='z10-02'; title='搜索投影、分页与快照恢复规则'; paths=@($pending | Where-Object {$_ -match '^internal/search/'}); remove=@(); packages=@('./internal/search'); tests=@('TestCanalFullRowsAndSafeProjection','TestCanalMySQLEnumOrdinal','TestHandlerOnlyAcknowledgesWholeMessage','TestIndexVersionConflict','TestSearchTenantPagingAndCursorBinding','TestSearchServiceUsesTrustedTenantAndReadiness','TestResetTaskIndexRefusesOtherResources')},
            @{id='z10-03'; title='独立服务、Canal与维护入口接线'; paths=@('*'); remove=@($stage.changes.removed); packages=@('./internal/platform','./internal/cli'); tests=@('TestSearchMethodPermissions','TestOpctlSearchCallsSearchRPC')}
        )
    } elseif ($stageID -eq 'z11') {
        $definitions = @(
            @{id='z11-01'; title='HTTP 映射与可复现生成'; paths=@($pending | Where-Object {$_ -match '^(proto/|gen/|go\.(mod|sum)$|scripts/(generate-http|verify-proto)\.ps1$)'}); remove=@(); packages=@('./gen/...'); tests=@()},
            @{id='z11-02'; title='个人账号、HTTP 查询与实时订阅'; paths=@('*'); remove=@($stage.changes.removed); packages=@('./internal/web','./internal/accounts','./internal/platform','./cmd/account-admin'); tests=@('TestSessionBoundary','TestSessionExpiresAndDoesNotReturnMachineToken','TestGeneratedGatewayAndStreamCancellation','TestPasswordPolicyAndArgonBounds','TestAccountMustChangeAndRevocation','TestAccountRejectsRoleSpoofAndUsesPersonalToken','TestPersonalResolverIdentityAndFailClosed','TestPasswordInputIsBoundedStrictAndPreservesSpaces')}
        )
    } elseif ($stageID -eq 'z12') {
        $definitions = @(
            @{id='z12-01'; title='前端领域类型、状态规则与依赖'; paths=@($pending | Where-Object {$_ -match '^web/(package.*\.json|tsconfig.*\.json|vite.config.ts|tests/|src/(api|accounts|domain|types|transport|requests|map|scene|trails)\.ts$)' -or $_ -eq 'scripts/frontend.ps1'}); remove=@(); packages=@(); tests=@()},
            @{id='z12-02'; title='会话、实体地图与模拟控制页面'; paths=@('*'); remove=@($stage.changes.removed); packages=@(); tests=@()}
        )
    } elseif ($stageID -eq 'z13') {
        $definitions = @(
            @{id='z13-01'; title='容器网络、持久初始化与统一启动'; paths=@('*'); remove=@($stage.changes.removed); packages=@('./internal/web','./internal/platform'); tests=@('TestSessionBoundary','TestGeneratedGatewayAndStreamCancellation')}
        )
    } else {
        $definitions = @(
            @{id='z07-01'; title='任务协议与纯业务规则'; paths=@($pending | Where-Object {$_ -match '^(proto/|gen/)'} )+@('internal/tasks/domain.go','internal/tasks/domain_test.go'); remove=@(); packages=@('./internal/tasks'); tests=@('TestInspectValidation','TestCreateHashUsesDeadlinePresenceNotClock','TestTaskTransitionAuthorityAndTerminalBarrier')},
            @{id='z07-02'; title='任务事务、查询与Outbox'; paths=@('internal/tasks/service.go','internal/tasks/store.go','internal/tasks/queries.go','internal/tasks/migrations.go','internal/tasks/workers.go','internal/tasks/domain_test.go','migrations/001_initial_schema.sql','migrations/002_framework_contracts.sql','migrations/003_implementation_contracts.sql','migrations/004_history_retention_indexes.sql'); remove=@(); packages=@('./internal/tasks'); tests=@('TestInspectValidation','TestTaskTransitionAuthorityAndTerminalBarrier','TestTaskCursorBoundToTenantFilterAndPosition','TestHistoricalMigrationsRemainExecutable')},
            @{id='z07-03'; title='持久分发与有界发送队列'; paths=@('internal/tasks/dispatcher.go','internal/tasks/dispatch_worker.go','internal/tasks/interfaces.go','internal/tasks/dispatch_bounds_test.go'); remove=@(); packages=@('./internal/tasks'); tests=@('TestExecutorQueueBoundsIdentityAndCommandDedup')},
            @{id='z07-04'; title='执行inbox与结果落盘'; paths=@('internal/edge/inbox.go','internal/edge/executor.go','internal/edge/inbox_test.go'); remove=@(); packages=@('./internal/edge'); tests=@('TestInboxCorruptionCannotEraseDurableEffects','TestInboxEffectAndReportSurviveRestart','TestInboxCancelBeforeExecuteSurvivesRestart','TestInboxCancelCompetesWithResultTransaction','TestInboxCapacityRejectsBeforeAcceptance')},
            @{id='z07-05'; title='四服务与模拟器完整装配'; paths=@('*'); remove=@($stage.changes.removed); packages=@('./internal/app','./internal/cli','./internal/edge'); tests=@('TestExecutorReportsSeriallyAndRetriesExactIdentity','TestExecutorRestartAfterResultBeforeReport')}
        )
    }
    foreach ($definition in $definitions) {
        $paths = if ($definition.paths -contains '*') { @($pending.ToArray()) } else { @($definition.paths) }
        $files = foreach ($path in $paths) {
            if ($path -notin $stage.files.path) { throw "Unknown file $stageID/$path" }
            [void]$pending.Remove($path)
            $source = if ($definition.id -eq 'z07-01' -and $path -eq 'internal/tasks/domain_test.go') { $override } else { $stage.source+'/'+$path }
            [ordered]@{path=$path; source=$source; sha256=(Get-FileHash (Join-Path $root $source)).Hash.ToLowerInvariant()}
        }
        $needs = if ($definition.ContainsKey('needs')) {@($definition.needs)} else {@()}
        $step = [ordered]@{id=$definition.id; stage=$stageID; title=$definition.title; removed=@($definition.remove); files=@($files); packages=@($definition.packages); tests=@($definition.tests); needs=$needs}
        $steps.Add($step)
        $lines = [Collections.Generic.List[string]]::new()
        $lines.Add('# '+$definition.id.ToUpper()+'｜'+$definition.title)
        $lines.Add('')
        $lines.Add('本页是小步骤的精确文件清单，配合[逐步操作说明](README.md)使用。所有路径相对于同一个学习目录；按表创建或整份替换，已有未列出的文件保留。完整文件直接链接到本步骤使用的真实版本，不需要拼接解释片段。')
        $lines.Add('')
        $lines.Add('## 先移除（先停止旧服务，备份放到工程外）')
        $lines.Add('')
        if ($step.removed.Count -eq 0) {$lines.Add('无。')}
        foreach($path in $step.removed){$lines.Add('- `'+$path+'`')}
        $lines.Add('')
        $lines.Add('## 创建或完整替换')
        $lines.Add('')
        $lines.Add('| 学习目录内路径 | 本步骤完整文件 |')
        $lines.Add('| --- | --- |')
        foreach($file in $files){$lines.Add('| `'+$file.path+'` | [复制完整文件](../../'+$file.source+') |')}
        $lines.Add('')
        $lines.Add('## 执行')
        $lines.Add('')
        if($needs.Count){$lines.Add('本步需要真实依赖：'+($needs -join '、')+'。先按[依赖接线说明](remaining.md)设置测试环境；SKIP或未发现指定测试均不算通过。');$lines.Add('')}
        $lines.Add('```powershell')
        $lines.Add("Set-Location 'D:\job\golang\projects\meshops-course-lab'")
        $lines.Add("`$env:Path = 'D:\go1.25.10\bin;' + `$env:Path")
        $lines.Add("`$env:GOWORK = 'off'")
        $lines.Add('go build ./...')
        $lines.Add("if (`$LASTEXITCODE -ne 0) { throw 'build failed' }")
        if($step.tests.Count) {
            $pattern = '^('+($step.tests -join '|')+')$'
            $lines.Add('go test '+($step.packages -join ' ')+' -run '''+$pattern+''' -count=1 -v')
            $lines.Add("if (`$LASTEXITCODE -ne 0) { throw 'test failed' }")
        } elseif ($step.id -eq 'z12-01') {
            $lines.Add('./scripts/frontend.ps1 -Action Install')
            $lines.Add('./scripts/frontend.ps1 -Action Test')
        } elseif ($step.id -eq 'z12-02') {
            $lines.Add('./scripts/frontend.ps1 -Action Install')
            $lines.Add('./scripts/frontend.ps1 -Action Test')
            $lines.Add('./scripts/frontend.ps1 -Action Build')
        } else {$lines.Add('go test ./gen/...')}
        $lines.Add('```')
        $lines.Add('')
        if($step.tests.Count){$lines.Add('构建成功通常没有输出。测试必须出现下列顶层测试的PASS；[no tests to run]不是通过本步骤。')}
        foreach($name in $step.tests){$lines.Add('- `'+$name+'`')}
        if($step.stage -eq 'z12'){$lines.Add('Node 24.15.0 运行领域测试；第二步还必须通过 TypeScript 检查并生成 web/dist。构建通过不能代替浏览器真实业务验收。')}
        elseif(-not $step.tests.Count){$lines.Add('本步骤只检查生成包，[no test files]是预期，不能声称业务请求已验证。')}
        [IO.File]::WriteAllText((Join-Path $output ($definition.id+'.md')),($lines -join "`n")+"`n",$utf8)
    }
    if($pending.Count){throw "Unassigned files: $pending"}
}
[IO.File]::WriteAllText((Join-Path $root 'substep-index.json'),($steps | ConvertTo-Json -Depth 8)+"`n",$utf8)
Write-Output "Published $($steps.Count) buildable substep specifications; actual build verification required."
