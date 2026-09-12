# Teacher maintenance only. Learners receive complete, materialized directories.
param([ValidateSet('state','business','all')][string]$Scope = 'all')
$ErrorActionPreference = 'Stop'
$ref = [IO.Path]::GetFullPath((Join-Path $PSScriptRoot 'business-stages/z09'))
$utf8 = [Text.UTF8Encoding]::new($false)
function Read-Reference([string]$Relative) { return [IO.File]::ReadAllText((Join-Path $ref $Relative)).Replace("`r`n", "`n") }
function Save-Stage([string]$StageRoot, [string]$Relative, [string]$Content) {
    $path = Join-Path $StageRoot $Relative
    New-Item -ItemType Directory -Force -Path (Split-Path $path -Parent) | Out-Null
    [IO.File]::WriteAllText($path, $Content, $utf8)
}
function Cut-Between([string]$Text, [string]$Start, [string]$End) {
    $a = $Text.IndexOf($Start, [StringComparison]::Ordinal)
    if ($a -lt 0) { throw "Missing slice start: $Start" }
    $b = $Text.IndexOf($End, $a + $Start.Length, [StringComparison]::Ordinal)
    if ($b -lt 0) { throw "Missing slice end: $End" }
    return $Text.Substring(0, $a) + $Text.Substring($b)
}
if ($Scope -in @('state','all')) {
    foreach ($stage in @('z04','z05','z06')) {
        $root = Join-Path $PSScriptRoot "state-stages/$stage"
        foreach ($path in @('internal/platform/auth.go','internal/platform/config.go','internal/platform/types.go','internal/state/ingest.go','internal/state/validation.go')) {
            Save-Stage $root $path (Read-Reference $path)
        }
        $registry = (Read-Reference 'internal/platform/registry.go').Replace('if !catalog["inspect"] {','if len(m.Catalog) > 0 && !catalog["inspect"] {').Replace('[]string{"operator", "admin", "task_service", "dispatcher_service"}', '[]string{"operator"}')
        Save-Stage $root 'internal/platform/registry.go' $registry
        $adapters = Read-Reference 'internal/state/adapters.go'
        if ($stage -ne 'z06') {
            $adapters = Cut-Between $adapters "`n`tcase `"vehicle`":" "`n`t}`n`tif err != nil"
            $adapters = $adapters.Replace('allowed, ok := keys[source.Adapter]', 'if source.Adapter != "person" && source.Adapter != "drone" { return nil, fmt.Errorf("adapter enters in Z06") }; allowed, ok := keys[source.Adapter]')
            $adapters = $adapters.Replace('the six documented wire formats', 'the two Z04 wire formats')
        }
        Save-Stage $root 'internal/state/adapters.go' $adapters
        if ($stage -eq 'z06') {
            foreach ($path in @('internal/state/adapters_test.go','internal/state/subscription.go','internal/edge/queue.go','internal/edge/uplink.go','internal/edge/simulation.go')) { Save-Stage $root $path (Read-Reference $path) }
        }
        if ($stage -ne 'z04') {
            foreach ($path in @('internal/bus/kafka.go','internal/bus/lag.go','internal/bus/retention.go')) {
                $content = (Read-Reference $path).Replace('[]string{"entity-state-events.v1", "task-events.v1", "task-dlq.v1"}', '[]string{"entity-state-events.v1"}')
                Save-Stage $root $path $content
            }
        }
        & gofmt -w (Join-Path $root 'internal')
        if ($LASTEXITCODE -ne 0) { throw "gofmt $stage failed" }
    }
}
if ($Scope -in @('business','all')) {
    # Explicit inputs prevent local secrets, binaries and previous reports entering lessons.
    $inputs = @('cmd','internal','proto','gen','configs','migrations','scripts','testdata')
    foreach ($stage in @('z07','z08')) {
        $root = Join-Path $PSScriptRoot "business-stages/$stage"
        $files = @('go.mod','go.sum','docker-compose.yml','.gitignore')
        foreach ($folder in $inputs) {
            foreach ($file in Get-ChildItem (Join-Path $ref $folder) -File -Recurse -Force) {
                $files += $file.FullName.Substring($ref.Length+1).Replace('\','/')
            }
        }
        foreach ($path in $files) {
            if ($path -match '^(cmd/verify/|internal/verification/)' -or $path -match '^scripts/(faults|benchmark)\.ps1$' -or $path -match 'integration-results\.txt$') { continue }
            if ($path -in @('internal/state/rebuild.go','internal/state/rebuild_integration_test.go')) { continue }
            if ($stage -eq 'z07' -and $path -match '^internal/state/(history.*|retention_integration_test)\.go$') { continue }
            $destination = Join-Path $root $path
            New-Item -ItemType Directory -Force -Path (Split-Path $destination -Parent) | Out-Null
            Copy-Item -LiteralPath (Join-Path $ref $path) -Destination $destination -Force
        }
        $app = Read-Reference 'internal/app/run.go'
        $app = Cut-Between $app "`trebuild := fs.Bool(" "`tif e := fs.Parse(args);"
        $app = Cut-Between $app "`t`tif *rebuild {" "`t`tregister = func(s *grpc.Server) { entityv1.RegisterEntityServiceServer"
        $app = Cut-Between $app "`tif *rebuild {" "`t// Workers and health"
        if ($stage -eq 'z07') { $app = $app.Replace('state.NewEntity(cfg.MeshOps, reg, redisClient, db)', 'state.NewEntity(cfg.MeshOps, reg, redisClient)') }
        Save-Stage $root 'internal/app/run.go' $app
        $build = (Read-Reference 'scripts/build.ps1').Replace(",'verify'",'')
        Save-Stage $root 'scripts/build.ps1' $build
        $tests = Read-Reference 'internal/state/state_test.go'
        $tests = Cut-Between $tests "`twant := map[string]ExpectedState" "`tif ttl, err := cache.TTL"
        $a = $tests.IndexOf("`twant[`"t:p`"] = ExpectedState", [StringComparison]::Ordinal)
        if ($a -lt 0) { throw 'Missing final manifest assertion' }
        $tests = $tests.Substring(0,$a) + "}`n"
        if ($stage -eq 'z07') {
            $tests = Cut-Between $tests 'func TestSamplingPriorityAndCircularHeading' '// This test uses only'
            $tests = $tests.Replace('TestRedisOrderingTombstoneAndManifest','TestRedisOrderingAndTombstone')
            $entity = Read-Reference 'internal/state/entity.go'
            foreach ($import in @('database/sql','errors','os')) { $entity = $entity.Replace("`t`"$import`"`n",'') }
            $a = $entity.IndexOf('type Entity struct {', [StringComparison]::Ordinal)
            $b = $entity.IndexOf('func (e *Entity) generation', [StringComparison]::Ordinal)
            if ($a -lt 0 -or $b -lt $a) { throw 'Missing Entity constructor boundaries' }
            $constructor = @'
// Z07 只包含当前状态和订阅，状态历史在 Z08 中加入。
type Entity struct {
    entityv1.UnimplementedEntityServiceServer
    cfg platform.Settings
    registry *platform.Registry
    redis *redis.Client
    mu sync.Mutex
    subscribers map[string]map[*subscription]struct{}
}
func NewEntity(cfg platform.Settings, r *platform.Registry, cache *redis.Client) (*Entity, error) {
    if cache == nil || r == nil { return nil, fmt.Errorf("entity requires registry and Redis") }
    if cfg.SubscriberQueueSize == 0 { cfg.SubscriberQueueSize = 1000 }
    if cfg.SubscriberMaxBytes == 0 { cfg.SubscriberMaxBytes = 8 << 20 }
    if cfg.ReconcileInterval == "" { cfg.ReconcileInterval = "10s" }
    if cfg.SlowConsumerTimeout == "" { cfg.SlowConsumerTimeout = "5s" }
    if cfg.SubscriberQueueSize < 1 || cfg.SubscriberQueueSize > 1000 || cfg.SubscriberMaxBytes < 1 || cfg.SubscriberMaxBytes > 8<<20 || platform.Duration(cfg.ReconcileInterval) <= 0 || platform.Duration(cfg.SlowConsumerTimeout) <= 0 { return nil, fmt.Errorf("invalid entity limits") }
    e := &Entity{cfg:cfg, registry:r, redis:cache, subscribers:map[string]map[*subscription]struct{}{}}
    ctx,cancel := context.WithTimeout(context.Background(),5*time.Second)
    defer cancel()
    if err := cache.SetNX(ctx,activeKey,newID(),0).Err(); err != nil { return nil,fmt.Errorf("initialize active view: %w",err) }
    return e,nil
}

'@
            $entity = $entity.Substring(0,$a) + $constructor + $entity.Substring($b)
            $a = $entity.IndexOf('func (e *Entity) Run(', [StringComparison]::Ordinal)
            if ($a -lt 0) { throw 'Missing Entity worker' }
            $entity = $entity.Substring(0,$a) + @'
func (e *Entity) Run(ctx context.Context, b Consumer) error {
    return b.Consume(ctx,e.cfg.ConsumerGroupPrefix+"entity-projector-v1",e.cfg.TopicPrefix+"entity-state-events.v1",e.Project)
}
'@
            Save-Stage $root 'internal/state/entity.go' $entity
            $cli = Read-Reference 'internal/cli/opctl.go'
            $cli = $cli.Replace('snapshot|subscribe|history|task','snapshot|subscribe|task').Replace('"history": true, ','').Replace('"snapshot", "history", "task create"','"snapshot", "task create"')
            $cli = Cut-Between $cli "`tstart := fs.String(" "`tduration := fs.Duration("
            $cli = $cli.Replace('var from, to, taskDeadline *timestamppb.Timestamp','var taskDeadline *timestamppb.Timestamp')
            $cli = Cut-Between $cli "`tif command == `"history`" {" "`tif command == `"task create`" {"
            $cli = $cli.Replace('if command == "history" || command == "task list" {','if command == "task list" {')
            $cli = $cli.Replace("`t`tif command == `"history`" {`n`t`t`tlimit = 500`n`t`t}`n",'')
            $cli = Cut-Between $cli "`tcase `"history`":`n" "`tcase `"task create`":`n"
            Save-Stage $root 'internal/cli/opctl.go' $cli
        }
        Save-Stage $root 'internal/state/state_test.go' $tests
        $readme = @"
# MeshOps $stage 完成检查点

这是可独立构建的阶段源码。module 为 example.com/meshops-course。
先停止上一阶段服务，在本目录运行 ./scripts/initialize.ps1、./scripts/start.ps1 -Simulators、./scripts/demo.ps1，结束运行 ./scripts/stop.ps1。
initialize 包含七个程序的构建、001—004迁移和六类来源/四类执行方 seed。

Z07 支持六类当前状态、订阅、持久补传、inspect 任务、取消/超时/DLQ；状态历史从 Z08 加入。
Z08 另支持状态历史抽样与查询。影子恢复、verify 压测和故障工具从 Z09 加入。
本阶段无 --rebuild-view 参数；Z07 无 opctl history 命令，协议中尚未实现的历史 RPC 返回 UNIMPLEMENTED。
迁移先保留完整编号，历史表是 Z08 的预备 schema，Z07 不写入历史样本；任务审计 history 不受影响。

本阶段使用最终专用 meshops-course Compose（MySQL13306、Redis16379、Kafka19092）。
从 Z06 的 meshops-state-lessons 环境升级时停止旧服务，重新 seed 并上报模拟状态；不会自动搬迁旧 Redis 视图。
在同一 learner 目录升级 Z07→Z08→Z09 会保留 .local 凭证、bbolt 文件和 Compose 数据。
这份目录是阶段答案，逐课详细讲义和全课程复制验收分别跟踪，不能据此声称已经学会。
"@
        Save-Stage $root 'README.md' $readme
        & gofmt -w (Join-Path $root 'internal') (Join-Path $root 'cmd')
        if ($LASTEXITCODE -ne 0) { throw "gofmt $stage failed" }
        "Materialized $stage."
    }
}
