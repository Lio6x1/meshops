// Package app 负责进程装配，业务规则位于 state/tasks 包。
package app

import (
	"context"
	"database/sql"
	"errors"
	dispatcherv1 "example.com/meshops-course/gen/dispatcher/v1"
	entityv1 "example.com/meshops-course/gen/entity/v1"
	executorv1 "example.com/meshops-course/gen/executor/v1"
	ingestv1 "example.com/meshops-course/gen/ingest/v1"
	taskv1 "example.com/meshops-course/gen/task/v1"
	"example.com/meshops-course/internal/bus"
	"example.com/meshops-course/internal/platform"
	"example.com/meshops-course/internal/state"
	"example.com/meshops-course/internal/tasks"
	"flag"
	"fmt"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	"github.com/zeromicro/go-zero/core/proc"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

func Main(role string) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	code := Run(ctx, role, os.Args[1:], os.Stderr)
	os.Exit(code)
}
func Run(parent context.Context, role string, args []string, diagnostics io.Writer) int {
	fs := flag.NewFlagSet(role, flag.ContinueOnError)
	fs.SetOutput(diagnostics)
	path := fs.String("f", "configs/"+role+".yaml", "service configuration")
	if e := fs.Parse(args); e != nil {
		return 2
	}
	if fs.NArg() != 0 {
		return 2
	}
	fail := func(e error) int { fmt.Fprintln(diagnostics, e); return 1 }
	cfg, e := platform.LoadConfig(*path)
	if e != nil {
		return fail(e)
	}
	reg, e := platform.LoadRegistry(cfg.MeshOps.Manifest)
	if e != nil {
		return fail(e)
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	var closers []func()
	defer func() {
		for i := len(closers) - 1; i >= 0; i-- {
			closers[i]()
		}
	}()
	kafka := bus.New(cfg.MeshOps.KafkaBrokers)
	closers = append(closers, func() { kafka.Close() })
	var db *sql.DB
	var redisClient *redis.Client
	if role != "ingest" {
		db, e = platform.OpenDB(cfg.MeshOps.MySQLDSNEnv)
		if e != nil {
			return fail(e)
		}
		closers = append(closers, func() { db.Close() })
		if e = tasks.CheckBindings(ctx, db, reg); e != nil {
			return fail(e)
		}
	}
	if role == "entity" {
		redisClient = redis.NewClient(&redis.Options{Addr: cfg.MeshOps.RedisAddr, Password: os.Getenv(cfg.MeshOps.RedisPasswordEnv), DialTimeout: 2 * time.Second, ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second})
		closers = append(closers, func() { redisClient.Close() })
		if e = redisClient.Ping(ctx).Err(); e != nil {
			return fail(errors.New("redis unavailable"))
		}
	}
	if e = kafka.Ping(ctx); e != nil {
		return fail(errors.New("kafka unavailable"))
	}
	if role == "task" || role == "dispatcher" {
		if len(os.Getenv(cfg.MeshOps.ServiceTokenEnv)) < 32 {
			return fail(errors.New("ServiceTokenEnv requires a credential"))
		}
	}
	dial := func(addr string) (*grpc.ClientConn, error) {
		c, e := platform.Dial(addr)
		if e == nil {
			closers = append(closers, func() { c.Close() })
		}
		return c, e
	}
	var register func(*grpc.Server)
	var worker func(context.Context) error
	switch role {
	case "ingest":
		service := state.NewIngest(cfg.MeshOps, reg, kafka)
		register = func(s *grpc.Server) { ingestv1.RegisterIngestServiceServer(s, service) }
	case "entity":
		service, e := state.NewEntity(cfg.MeshOps, reg, redisClient)
		if e != nil {
			return fail(e)
		}
		register = func(s *grpc.Server) { entityv1.RegisterEntityServiceServer(s, service) }
		worker = func(c context.Context) error { return service.Run(c, kafka) }
	case "task":
		ec, e := dial(cfg.MeshOps.EntityEndpoint)
		if e != nil {
			return fail(e)
		}
		dc, e := dial(cfg.MeshOps.DispatcherEndpoint)
		if e != nil {
			return fail(e)
		}
		service, e := tasks.NewService(cfg.MeshOps, reg, db, entityv1.NewEntityServiceClient(ec), dispatcherv1.NewDispatcherServiceClient(dc))
		if e != nil {
			return fail(e)
		}
		register = func(s *grpc.Server) { taskv1.RegisterTaskServiceServer(s, service) }
		worker = func(c context.Context) error { return service.Run(c, kafka) }
	case "dispatcher":
		tc, e := dial(cfg.MeshOps.TaskEndpoint)
		if e != nil {
			return fail(e)
		}
		service, e := tasks.NewDispatcher(cfg.MeshOps, reg, db, taskv1.NewTaskServiceClient(tc))
		if e != nil {
			return fail(e)
		}
		service.SetLagReader(kafka.Lag)
		register = func(s *grpc.Server) {
			dispatcherv1.RegisterDispatcherServiceServer(s, service)
			executorv1.RegisterExecutorServiceServer(s, service)
		}
		worker = func(c context.Context) error { return service.Run(c, kafka) }
	default:
		return 2
	}
	// 工作协程与健康检查共享生命周期；就绪检查只检测直接依赖。
	var ready atomic.Bool
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/livez", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("live\n")) })
	checks := []func(context.Context) error{kafka.Ping}
	if db != nil {
		checks = append(checks, db.PingContext)
	}
	if redisClient != nil {
		checks = append(checks, func(ctx context.Context) error { return redisClient.Ping(ctx).Err() })
	}
	mux.HandleFunc("/readyz", readiness(&ready, checks...))
	// Metrics/pprof 绑定回环 IP，作为本地运维端点。
	host, _, e := net.SplitHostPort(cfg.MeshOps.MetricsAddr)
	if e != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return fail(errors.New("MetricsAddr must use loopback IP"))
	}
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	listener, e := net.Listen("tcp", cfg.MeshOps.MetricsAddr)
	if e != nil {
		return fail(e)
	}
	httpServer := &http.Server{Handler: mux, ReadHeaderTimeout: 3 * time.Second}
	closers = append(closers, func() { httpServer.Close() })
	failures := make(chan error, 3)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if e := httpServer.Serve(listener); e != nil && !errors.Is(e, http.ErrServerClosed) {
			failures <- e
		}
	}()
	if worker != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := worker(ctx); ctx.Err() == nil {
				if e == nil {
					e = errors.New("background worker stopped unexpectedly")
				}
				failures <- e
			}
		}()
	}
	cfg.Timeout = platform.Duration(cfg.MeshOps.UnaryTimeout).Milliseconds()
	cfg.Middlewares.Stat = false
	var grpcServer atomic.Pointer[grpc.Server]
	rpc, e := zrpc.NewServer(cfg.RpcServerConf, func(s *grpc.Server) { register(s); grpcServer.Store(s); ready.Store(true) })
	if e != nil {
		cancel()
		httpServer.Close()
		wg.Wait()
		return fail(e)
	}
	rpc.AddUnaryInterceptors(reg.Unary())
	rpc.AddStreamInterceptors(reg.Stream())
	rpc.AddOptions(grpc.MaxRecvMsgSize(cfg.MeshOps.MaxMessageBytes), grpc.MaxSendMsgSize(cfg.MeshOps.MaxMessageBytes))
	proc.SetTimeToForceQuit(platform.Duration(cfg.MeshOps.ShutdownTimeout) + 2*time.Second)
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			if v := recover(); v != nil {
				failures <- fmt.Errorf("RPC startup failed: %v", v)
			}
		}()
		rpc.Start()
	}()
	slog.Info("service started", "role", role, "rpc", cfg.ListenOn, "metrics", cfg.MeshOps.MetricsAddr)
	var result error
	select {
	case <-parent.Done():
	case result = <-failures:
	}
	ready.Store(false)
	cancel()
	httpServer.Close()
	// go-zero 会安装进程关闭监听器，显式取消时也要触发这些监听器。
	done := make(chan struct{})
	go func() {
		if s := grpcServer.Load(); s != nil {
			s.GracefulStop()
		}
		proc.Shutdown()
		close(done)
	}()
	timer := time.NewTimer(platform.Duration(cfg.MeshOps.ShutdownTimeout))
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		if s := grpcServer.Load(); s != nil {
			s.Stop()
		}
	}
	drained := make(chan struct{})
	go func() { wg.Wait(); close(drained) }()
	select {
	case <-drained:
	case <-time.After(2 * time.Second):
		return fail(errors.New("shutdown exceeded bound"))
	}
	rpc.Stop()
	if result != nil {
		return fail(result)
	}
	return 0
}
