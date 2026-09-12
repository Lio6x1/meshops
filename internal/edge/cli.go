package edge

import (
	"context"
	"encoding/json"
	"errors"
	commonv1 "example.com/meshops-course/gen/common/v1"
	executorv1 "example.com/meshops-course/gen/executor/v1"
	ingestv1 "example.com/meshops-course/gen/ingest/v1"
	taskv1 "example.com/meshops-course/gen/task/v1"
	"example.com/meshops-course/internal/platform"
	"example.com/meshops-course/internal/simulation"
	"example.com/meshops-course/internal/state"
	"flag"
	"fmt"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"io"
	"math/rand"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

func endpoint(env, def string) string {
	if v := os.Getenv(env); v != "" {
		return v
	}
	return def
}
func cliError(w io.Writer, err error) int {
	code := status.Code(err)
	// 112 仅在 Windows 是 ERROR_DISK_FULL；其他系统的同值 errno 不是磁盘耗尽。
	const windowsDiskFull syscall.Errno = 112
	if errors.Is(err, syscall.ENOSPC) || runtime.GOOS == "windows" && errors.Is(err, windowsDiskFull) {
		code = codes.ResourceExhausted
	}
	if code == codes.Unknown {
		code = codes.Internal
	}
	name := regexp.MustCompile(`([a-z])([A-Z])`).ReplaceAllString(code.String(), "${1}_${2}")
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": strings.ToUpper(name), "message": err.Error()}})
	return 1
}
func usage(w io.Writer, msg string) int { fmt.Fprintln(w, msg); return 2 }
func credential(r *platform.Registry, tenant, id, env string) (string, error) {
	if env == "" {
		return r.Credential(tenant, id)
	}
	if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(env) {
		return "", errors.New("token-env must be an environment variable name")
	}
	token := os.Getenv(env)
	if token == "" {
		return "", fmt.Errorf("environment variable %s is empty", env)
	}
	return token, nil
}
func GatewayCLI(ctx context.Context, args []string, out, diagnostic io.Writer) int {
	f := flag.NewFlagSet("gateway-simulator", flag.ContinueOnError)
	f.SetOutput(diagnostic)
	manifest := f.String("manifest", "", "manifest YAML path(s)")
	sourceID := f.String("source", "", "registered source ID")
	dbPath := f.String("db", "", "exclusive bbolt database path")
	rate := f.Int("rate", 20, "generated events/second")
	batch := f.Int("batch", 100, "upload batch 1..100")
	ep := f.String("endpoint", endpoint("MESHOPS_INGEST_ENDPOINT", "127.0.0.1:50051"), "Ingest endpoint")
	tokenEnv := f.String("token-env", "", "credential environment variable name")
	duration := f.Duration("duration", 60*time.Second, "generation duration")
	timeout := f.Duration("timeout", 60*time.Second, "drain timeout")
	offline := f.Bool("offline", false, "generate without network")
	drain := f.Bool("drain-only", false, "upload without generating")
	compact := f.Bool("compact", false, "offline compaction preserving backup")
	count := f.Int64("count", 0, "stop after exactly this many generated events (0 uses duration)")
	recovery := f.Int("recovery-rate", 500, "maximum replay events/second")
	duplicate := f.Int64("duplicate-every", 0, "enqueue an unchanged duplicate every N generated events")
	seed := f.Int64("seed", 1, "deterministic entity selection seed")
	if e := f.Parse(args); e != nil {
		if e == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if f.NArg() != 0 {
		return usage(diagnostic, "unexpected positional arguments")
	}
	if *dbPath == "" {
		return usage(diagnostic, "--db is required")
	}
	modes := 0
	for _, b := range []bool{*offline, *drain, *compact} {
		if b {
			modes++
		}
	}
	if modes > 1 {
		return usage(diagnostic, "offline, drain-only, compact are mutually exclusive")
	}
	if *rate < 1 || *rate > 1000000 || *batch < 1 || *batch > 100 || *duration <= 0 || *timeout <= 0 || *count < 0 || *duplicate < 0 || *recovery < 1 || *recovery > 1000000 {
		return usage(diagnostic, "rate, batch, duration, timeout, count, duplicate-every or recovery-rate is invalid")
	}
	if *compact {
		backup, e := CompactQueue(*dbPath)
		if e != nil {
			return cliError(out, e)
		}
		_ = json.NewEncoder(out).Encode(map[string]any{"compacted": true, "backup": backup})
		return 0
	}
	if *manifest == "" || *sourceID == "" {
		return usage(diagnostic, "--manifest and --source are required")
	}
	r, e := platform.LoadRegistry(*manifest)
	if e != nil {
		return cliError(out, e)
	}
	var source platform.Source
	for _, s := range r.Sources {
		if s.ID == *sourceID {
			source = s
			break
		}
	}
	if source.ID == "" {
		return cliError(out, status.Error(codes.NotFound, "source is not registered"))
	}
	token, e := credential(r, source.TenantID, source.ID, *tokenEnv)
	if e != nil {
		return cliError(out, e)
	}
	q, e := OpenQueue(*dbPath)
	if e != nil {
		return cliError(out, e)
	}
	defer q.Close()
	if e = q.BindSource(source.TenantID + ":" + source.ID + ":" + fmt.Sprint(source.Generation)); e != nil {
		return cliError(out, e)
	}
	var stats UplinkStats
	var generated atomic.Int64
	initial, e := q.Stats()
	if e != nil {
		return cliError(out, e)
	}
	configuredNet := *recovery - *rate
	if *drain {
		configuredNet = *recovery
	}
	if *offline {
		configuredNet = -*rate
	}
	started := time.Now()
	printStats := func() {
		s, err := q.Stats()
		if err != nil {
			fmt.Fprintln(diagnostic, err)
			return
		}
		elapsed := time.Since(started).Seconds()
		_ = json.NewEncoder(out).Encode(map[string]any{"generated": generated.Load(), "sent": stats.Sent.Load(), "confirmedSequence": s.ConfirmedSequence, "pending": s.Pending, "pendingBytes": s.PendingBytes, "fileBytes": s.FileBytes, "reconnects": stats.Reconnects.Load(), "epoch": s.Epoch, "netDrainRate": float64(initial.Pending-s.Pending) / elapsed, "configuredNetDrainRate": configuredNet})
		if s.FileBytes > 2<<30 {
			fmt.Fprintln(diagnostic, "queue file exceeds 2GiB disk guard; schedule offline compaction")
		}
	}
	defer printStats()
	var client ingestv1.IngestServiceClient
	if !*offline {
		conn, err := platform.Dial(*ep)
		if err != nil {
			return cliError(out, err)
		}
		defer conn.Close()
		client = ingestv1.NewIngestServiceClient(conn)
	}
	if *drain {
		drainCtx, cancel := context.WithTimeout(platform.Outgoing(ctx, token), *timeout)
		defer cancel()
		if e = Upload(drainCtx, q, client, *batch, *recovery, true, &stats); e != nil {
			return cliError(out, e)
		}
		return 0
	}
	runCtx, cancel := context.WithCancel(platform.Outgoing(ctx, token))
	defer cancel()
	uploadResult := make(chan error, 1)
	genCtx, stop := context.WithTimeout(ctx, *duration)
	defer stop()
	ticker := time.NewTicker(time.Second / time.Duration(*rate))
	defer ticker.Stop()
	periodic := time.NewTicker(5 * time.Second)
	defer periodic.Stop()
	ids := make([]string, 0, len(source.Entities))
	for rawID := range source.Entities {
		ids = append(ids, rawID)
	}
	sort.Strings(ids)
	rng := rand.New(rand.NewSource(*seed))
	generate := func() error {
		rawID := ids[rng.Intn(len(ids))]
		var original *commonv1.EntityStateEvent
		_, err := q.Generate(platform.Key(source.TenantID, source.Entities[rawID]), func(version int64) (*commonv1.EntityStateEvent, error) {
			now := time.Now().UTC()
			raw, err := state.GenerateRaw(source, rawID, version, now)
			if err != nil {
				return nil, err
			}
			original, err = state.Normalize(raw, source, now)
			if err == nil {
				simulateMotion(original, rng)
			}
			return original, err
		})
		if err != nil {
			return err
		}
		n := generated.Add(1)
		if *duplicate > 0 && n%*duplicate == 0 {
			_, err = q.Enqueue(original)
		}
		return err
	}
	// Browser controls are an explicit demo opt-in. Offline/drain/compact CLI
	// exercises keep their original semantics and never consult this control plane.
	if os.Getenv("MESHOPS_SIMULATION_CONTROL") == "1" && !*offline {
		cache := redis.NewClient(&redis.Options{Addr: endpoint("MESHOPS_REDIS_ADDR", "127.0.0.1:16379"), DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, MaxRetries: -1, ContextTimeoutEnabled: true})
		defer cache.Close()
		controlledCtx, controlledCancel := context.WithCancel(platform.Outgoing(genCtx, token))
		defer controlledCancel()
		err := runControlledSimulation(controlledCtx, simulation.NewStore(cache), source.TenantID, source.ID, 500*time.Millisecond, time.Second/time.Duration(*rate), func() error {
			// A ready generation tick may win select after cancellation. Preserve
			// the CLI's exact count contract even at very high configured rates.
			if *count > 0 && generated.Load() >= *count {
				return nil
			}
			if err := generate(); err != nil {
				return err
			}
			if *count > 0 && generated.Load() >= *count {
				controlledCancel()
			}
			return nil
		}, func(ctx context.Context) error { return Upload(ctx, q, client, *batch, *recovery, false, &stats) }, func() simulation.Observation {
			s, err := q.Stats()
			if err != nil {
				fmt.Fprintln(diagnostic, err)
			}
			return simulation.Observation{Generated: strconv.FormatInt(generated.Load(), 10), Sent: strconv.FormatInt(stats.Sent.Load(), 10), Pending: strconv.Itoa(s.Pending)}
		}, diagnostic)
		if err != nil {
			return cliError(out, err)
		}
		if *count > 0 && generated.Load() < *count && ctx.Err() == nil {
			return cliError(out, status.Error(codes.DeadlineExceeded, "duration elapsed before requested count"))
		}
		return 0
	}
	if !*offline {
		go func() { uploadResult <- Upload(runCtx, q, client, *batch, *recovery, false, &stats) }()
	}
	var runErr error
	uploadConsumed := false
generation:
	for {
		select {
		case <-genCtx.Done():
			break generation
		case err := <-uploadResult:
			uploadConsumed = true
			runErr = err
			cancel()
			break generation
		case <-periodic.C:
			printStats()
		case <-ticker.C:
			if err := generate(); err != nil {
				runErr = err
				break generation
			}
			if *count > 0 && generated.Load() >= *count {
				break generation
			}
		}
	}
	cancel()
	if !*offline && !uploadConsumed {
		// 结束生成仍须观察上传协程的最终结果，避免同时发生的鉴权/协议错误被
		// count/duration 分支吞掉；仅本次主动关闭引起的取消可以视为正常退出。
		uploadErr := joinUpload(uploadResult)
		if runErr == nil {
			runErr = uploadErr
		}
	}
	if runErr != nil {
		return cliError(out, runErr)
	}
	if *count > 0 && generated.Load() < *count && ctx.Err() == nil {
		return cliError(out, status.Error(codes.DeadlineExceeded, "duration elapsed before requested count"))
	}
	return 0
}

func joinUpload(result <-chan error) error {
	err := <-result
	if errors.Is(err, context.Canceled) || status.Code(err) == codes.Canceled {
		return nil
	}
	return err
}

func ExecutorCLI(ctx context.Context, args []string, out, diagnostic io.Writer) int {
	f := flag.NewFlagSet("executor-simulator", flag.ContinueOnError)
	f.SetOutput(diagnostic)
	manifest := f.String("manifest", "", "manifest YAML path(s)")
	executor := f.String("executor", "", "registered executor ID")
	dbPath := f.String("db", "", "exclusive bbolt database")
	ep := f.String("endpoint", endpoint("MESHOPS_DISPATCHER_ENDPOINT", "127.0.0.1:50054"), "Dispatcher endpoint")
	taskEP := f.String("task-endpoint", endpoint("MESHOPS_TASK_ENDPOINT", "127.0.0.1:50053"), "Task endpoint")
	tokenEnv := f.String("token-env", "", "credential environment variable name")
	concurrency := f.Int("concurrency", 4, "maximum accepted active tasks 1..4")
	mode := f.String("mode", "normal", "normal, fail, reject")
	duration := f.Duration("duration", 0, "optional run duration (0 waits for signal)")
	if e := f.Parse(args); e != nil {
		if e == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if f.NArg() != 0 || *manifest == "" || *executor == "" || *dbPath == "" || *concurrency < 1 || *concurrency > 4 || *duration < 0 || (*mode != "normal" && *mode != "fail" && *mode != "reject") {
		return usage(diagnostic, "manifest, executor, db are required; concurrency 1..4, mode normal/fail/reject, duration nonnegative")
	}
	r, e := platform.LoadRegistry(*manifest)
	if e != nil {
		return cliError(out, e)
	}
	var principal platform.Principal
	for _, p := range r.Principals {
		if p.Role == "executor" && p.ExecutorID == *executor {
			if principal.ID != "" {
				return cliError(out, errors.New("executor ID is ambiguous across manifests"))
			}
			principal = p
		}
	}
	if principal.ID == "" {
		return cliError(out, status.Error(codes.NotFound, "executor is not registered"))
	}
	token, e := credential(r, principal.TenantID, principal.ID, *tokenEnv)
	if e != nil {
		return cliError(out, e)
	}
	inbox, e := OpenInbox(*dbPath)
	if e != nil {
		return cliError(out, e)
	}
	defer inbox.Close()
	if e = inbox.BindExecutor(platform.Key(principal.TenantID, principal.ExecutorID)); e != nil {
		return cliError(out, e)
	}
	commands, e := platform.Dial(*ep)
	if e != nil {
		return cliError(out, e)
	}
	defer commands.Close()
	tasks, e := platform.Dial(*taskEP)
	if e != nil {
		return cliError(out, e)
	}
	defer tasks.Close()
	runCtx := platform.Outgoing(ctx, token)
	if *duration > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(runCtx, *duration)
		defer cancel()
	}
	stats := new(ExecutorStats)
	e = RunExecutor(runCtx, inbox, executorv1.NewExecutorServiceClient(commands), taskv1.NewTaskServiceClient(tasks), *executor, *mode, *concurrency, stats)
	accepted, completed, effects, statsErr := inbox.Stats()
	_ = json.NewEncoder(out).Encode(map[string]any{"accepted": accepted, "completed": completed, "duplicateCommands": stats.DuplicateCommands.Load(), "invalidCommands": stats.InvalidCommands.Load(), "effectCount": effects})
	if statsErr != nil {
		return cliError(out, statsErr)
	}
	if e != nil && !errors.Is(e, context.Canceled) && !errors.Is(e, context.DeadlineExceeded) {
		return cliError(out, e)
	}
	return 0
}
