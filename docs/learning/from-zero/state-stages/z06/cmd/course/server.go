package main

import (
	"context"
	entityv1 "example.com/meshops-course/gen/entity/v1"
	ingestv1 "example.com/meshops-course/gen/ingest/v1"
	"example.com/meshops-course/internal/bus"
	"example.com/meshops-course/internal/platform"
	"example.com/meshops-course/internal/state"
	"fmt"
	"github.com/redis/go-redis/v9"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"
)

func serve(path, role string) error {
	cfg, err := platform.LoadConfig(path)
	if err != nil {
		return err
	}
	r, err := platform.LoadRegistry(cfg.MeshOps.Manifest)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	var register func(*grpc.Server)
	k := bus.New(cfg.MeshOps.KafkaBrokers)
	defer k.Close()
	if err = k.Ping(ctx); err != nil {
		return err
	}
	if err = k.EnsureTopics(ctx, cfg.MeshOps.TopicPrefix); err != nil {
		return err
	}
	switch role {
	case "ingest":
		ingest := state.NewIngest(cfg.MeshOps, r, k)
		register = func(s *grpc.Server) { ingestv1.RegisterIngestServiceServer(s, ingest) }
	case "entity":
		cache := redis.NewClient(&redis.Options{Addr: cfg.MeshOps.RedisAddr})
		defer cache.Close()
		entity, e := state.NewEntity(cfg.MeshOps, r, cache)
		if e != nil {
			return e
		}
		register = func(s *grpc.Server) { entityv1.RegisterEntityServiceServer(s, entity) }
		workerDone := make(chan struct{})
		defer func() { cancel(); <-workerDone }()
		go func() {
			defer close(workerDone)
			if e := entity.Run(ctx, k); ctx.Err() == nil {
				fmt.Fprintln(os.Stderr, e)
				cancel()
			}
		}()
	default:
		return fmt.Errorf("role must be ingest or entity")
	}
	var underlying atomic.Pointer[grpc.Server]
	cfg.Middlewares.Stat = false
	rpc, err := zrpc.NewServer(cfg.RpcServerConf, func(s *grpc.Server) { register(s); underlying.Store(s) })
	if err != nil {
		return err
	}
	rpc.AddUnaryInterceptors(r.Unary())
	rpc.AddStreamInterceptors(r.Stream())
	rpc.AddOptions(grpc.MaxRecvMsgSize(4<<20), grpc.MaxSendMsgSize(4<<20))
	done := make(chan struct{})
	go func() { defer close(done); rpc.Start() }()
	fmt.Println("RPC", cfg.ListenOn, "role", role)
	<-ctx.Done()
	if s := underlying.Load(); s != nil {
		s.Stop()
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		return fmt.Errorf("RPC shutdown timeout")
	}
	return nil
}
