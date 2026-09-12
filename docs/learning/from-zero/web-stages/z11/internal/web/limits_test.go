package web

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	entityv1 "example.com/meshops-course/gen/entity/v1"
	"example.com/meshops-course/internal/platform"
	"google.golang.org/grpc"
)

type boundedStreamProbe struct {
	entityv1.UnimplementedEntityServiceServer
	stopped chan struct{}
}

func (p *boundedStreamProbe) Subscribe(_ *entityv1.SubscribeRequest, stream entityv1.EntityService_SubscribeServer) error {
	if err := stream.Send(&entityv1.EntityUpdate{Kind: entityv1.EntityUpdateKind_ENTITY_UPDATE_KIND_SNAPSHOT_END}); err != nil {
		return err
	}
	<-stream.Context().Done()
	p.stopped <- struct{}{}
	return stream.Context().Err()
}
func streamFixture(t *testing.T) (*Server, *http.Cookie, *httptest.Server, *boundedStreamProbe) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	probe := &boundedStreamProbe{stopped: make(chan struct{}, 8)}
	rpc := grpc.NewServer()
	entityv1.RegisterEntityServiceServer(rpc, probe)
	go func() { _ = rpc.Serve(listener) }()
	t.Cleanup(rpc.Stop)
	conn, err := platform.Dial(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	base, _ := fixture(t)
	cfg := base.cfg
	cfg.Entity = entityv1.NewEntityServiceClient(conn)
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	cookie, _ := login(t, s, "operator")
	httpServer := httptest.NewServer(s.Handler())
	t.Cleanup(httpServer.Close)
	return s, cookie, httpServer, probe
}
func openStream(t *testing.T, server *httptest.Server, cookie *http.Cookie, ctx context.Context) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/v1/entities/stream?entity_ids=drone-001", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "localhost:18090"
	req.AddCookie(cookie)
	response, err := server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}
func TestStreamCapacityAndClientDisconnect(t *testing.T) {
	_, cookie, server, probe := streamFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	first := openStream(t, server, cookie, ctx)
	second := openStream(t, server, cookie, ctx)
	third := openStream(t, server, cookie, ctx)
	if first.StatusCode != 200 || second.StatusCode != 200 || third.StatusCode != 429 {
		t.Fatalf("statuses=%d,%d,%d", first.StatusCode, second.StatusCode, third.StatusCode)
	}
	_ = first.Body.Close()
	select {
	case <-probe.stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("browser disconnect did not cancel gRPC")
	}
	_ = second.Body.Close()
}
func TestStreamExpiresWithoutAnotherHTTPRequest(t *testing.T) {
	s, cookie, server, probe := streamFixture(t)
	// A short remaining lifetime avoids a minute-long test while exercising the
	// real transport timer rather than triggering expiry with a second request.
	s.mu.Lock()
	s.sessions[cookie.Value].expires = time.Now().Add(150 * time.Millisecond)
	s.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response := openStream(t, server, cookie, ctx)
	if response.StatusCode != 200 {
		t.Fatalf("status=%d", response.StatusCode)
	}
	select {
	case <-probe.stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("expired session left its stream alive")
	}
}
