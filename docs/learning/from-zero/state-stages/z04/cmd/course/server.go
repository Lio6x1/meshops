package main

import (
	"context"
	entityv1 "example.com/meshops-course/gen/entity/v1"
	ingestv1 "example.com/meshops-course/gen/ingest/v1"
	"example.com/meshops-course/internal/platform"
	"example.com/meshops-course/internal/state"
	"fmt"
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
	if role != "all" {
		return fmt.Errorf("Z04 role must be all")
	}
	memory := state.NewMemoryEntity(r)
	ingest := state.NewIngest(cfg.MeshOps, r, memory)
	register = func(s *grpc.Server) {
		entityv1.RegisterEntityServiceServer(s, memory)
		ingestv1.RegisterIngestServiceServer(s, ingest)
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
