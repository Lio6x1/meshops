package state

import (
	"context"
	entityv1 "example.com/meshops-course/gen/entity/v1"
	"example.com/meshops-course/internal/platform"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type measuredStream struct {
	grpc.ServerStream
	ctx    context.Context
	active *atomic.Int64
}

func (s measuredStream) Context() context.Context { return s.ctx }
func (s measuredStream) SendMsg(v any) error {
	s.active.Add(1)
	defer s.active.Add(-1)
	return s.ServerStream.SendMsg(v)
}

func TestGRPCBlockedSubscriberReleasesSenderAndDoesNotBlockPeer(t *testing.T) {
	addr := os.Getenv("MESHOPS_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("requires real Redis")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cache := redis.NewClient(&redis.Options{Addr: addr, DB: 15})
	defer cache.Close()
	generation := newID()
	if err := cache.SetNX(ctx, activeKey, generation, 0).Err(); err != nil {
		t.Fatal(err)
	}
	generation, err := cache.Get(ctx, activeKey).Result()
	if err != nil {
		t.Fatal(err)
	}
	registry, _ := ingestFixture(t)
	tenant := "transport_" + newID()
	e := &Entity{redis: cache, registry: registry, cfg: platform.Settings{SubscriberMaxBytes: 8 << 20, SubscriberQueueSize: 1000, SlowConsumerTimeout: "300ms", ReconcileInterval: "1h"}, subscribers: map[string]map[*subscription]struct{}{}}
	var slowActive, fastActive atomic.Int64
	slowDone := make(chan error, 1)
	server := grpc.NewServer(grpc.StreamInterceptor(func(srv any, stream grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		md, _ := metadata.FromIncomingContext(stream.Context())
		slow := len(md.Get("test-subscriber")) > 0 && md.Get("test-subscriber")[0] == "slow"
		active := &fastActive
		if slow {
			active = &slowActive
		}
		c := platform.WithPrincipal(stream.Context(), platform.Principal{TenantID: tenant, Role: "operator"})
		err := handler(srv, measuredStream{stream, c, active})
		if slow {
			slowDone <- err
		}
		return err
	}))
	entityv1.RegisterEntityServiceServer(server, e)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve(listener)
	defer server.Stop()
	defer listener.Close()
	connect := func(name string) entityv1.EntityService_SubscribeClient {
		conn, err := platform.Dial(listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		c := metadata.AppendToOutgoingContext(ctx, "test-subscriber", name)
		stream, err := entityv1.NewEntityServiceClient(conn).Subscribe(c, &entityv1.SubscribeRequest{EntityIds: []string{"p"}})
		if err != nil {
			t.Fatal(err)
		}
		frame, err := stream.Recv()
		if err != nil || frame.Kind != entityv1.EntityUpdateKind_ENTITY_UPDATE_KIND_SNAPSHOT_END {
			t.Fatal("initial snapshot", err)
		}
		return stream
	}
	_ = connect("slow") // Keep transport open, deliberately stop Recv after initialization.
	fast := connect("fast")
	seen := make(chan int64, 100)
	go func() {
		for {
			frame, err := fast.Recv()
			if err != nil {
				return
			}
			select {
			case seen <- frame.Version:
			case <-ctx.Done():
				return
			}
		}
	}()
	// Large transport fixtures force HTTP/2 stream flow control quickly. Domain
	// input validation is tested separately; no service Send implementation is mocked.
	payload := strings.Repeat("x", 256<<10)
	for version := int64(1); version <= 20; version++ {
		e.notify(tenant, generation, &entityv1.EntityUpdate{EntityId: "p", Kind: entityv1.EntityUpdateKind_ENTITY_UPDATE_KIND_UPSERT, Version: version, SourceId: payload})
		time.Sleep(40 * time.Millisecond)
	}
	select {
	case err := <-slowDone:
		if status.Code(err) != codes.ResourceExhausted {
			t.Fatal("slow subscriber wrong termination", err)
		}
	case <-ctx.Done():
		t.Fatal("slow handler leaked")
	}
	deadline := time.Now().Add(5 * time.Second)
	for slowActive.Load() != 0 {
		if time.Now().After(deadline) {
			t.Fatal("blocked SendMsg survived handler return")
		}
		time.Sleep(10 * time.Millisecond)
	}
	for {
		select {
		case version := <-seen:
			if version == 20 {
				goto verified
			}
		case <-ctx.Done():
			t.Fatal("healthy peer stopped receiving")
		}
	}
verified:
	e.mu.Lock()
	n := len(e.subscribers[tenant])
	e.mu.Unlock()
	if n != 1 {
		t.Fatal("slow subscriber registration leaked", n)
	}
	cancel()
	for {
		e.mu.Lock()
		n = len(e.subscribers[tenant])
		e.mu.Unlock()
		if n == 0 && fastActive.Load() == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cancellation leaked subscription")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
