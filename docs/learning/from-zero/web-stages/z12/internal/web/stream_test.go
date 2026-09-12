package web

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	entityv1 "example.com/meshops-course/gen/entity/v1"
	"example.com/meshops-course/internal/platform"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type entityProbe struct {
	entityv1.UnimplementedEntityServiceServer
	cancelled chan struct{}
}

func (p *entityProbe) GetSnapshot(ctx context.Context, r *entityv1.GetSnapshotRequest) (*entityv1.GetSnapshotResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	if strings.Join(md.Get("authorization"), ",") != "Bearer "+strings.Repeat("operator", 12) {
		return nil, status.Error(codes.Unauthenticated, "wrong propagated identity")
	}
	return &entityv1.GetSnapshotResponse{EntityId: r.EntityId, Version: 9007199254740991, Found: true}, nil
}
func (p *entityProbe) Subscribe(_ *entityv1.SubscribeRequest, stream entityv1.EntityService_SubscribeServer) error {
	if e := stream.Send(&entityv1.EntityUpdate{Kind: entityv1.EntityUpdateKind_ENTITY_UPDATE_KIND_SNAPSHOT_END, SyncId: "one", ViewGeneration: "view"}); e != nil {
		return e
	}
	<-stream.Context().Done()
	close(p.cancelled)
	return stream.Context().Err()
}
func TestGeneratedGatewayAndStreamCancellation(t *testing.T) {
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	probe := &entityProbe{cancelled: make(chan struct{})}
	rpc := grpc.NewServer()
	entityv1.RegisterEntityServiceServer(rpc, probe)
	go func() { _ = rpc.Serve(l) }()
	defer rpc.Stop()
	conn, e := platform.Dial(l.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	original, _ := fixture(t)
	cfg := original.cfg
	cfg.Entity = entityv1.NewEntityServiceClient(conn)
	s, e := New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	cookie, csrf := login(t, s, "operator")
	w := request(s, "GET", "/api/v1/entities/drone-001", "", cookie, "", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"version":"9007199254740991"`) {
		t.Fatalf("generated mapping %d %s", w.Code, w.Body.String())
	}
	// 调用方传入的 Authorization 请求头不能替换浏览器会话身份。
	req := httptest.NewRequest("GET", "http://localhost:18090/api/v1/entities/drone-001", nil)
	req.AddCookie(cookie)
	req.Header.Set("Authorization", "Bearer attacker")
	req.Header.Set("Grpc-Metadata-Authorization", "Bearer attacker")
	out := httptest.NewRecorder()
	s.Handler().ServeHTTP(out, req)
	if out.Code != 200 {
		t.Fatalf("metadata injection %d", out.Code)
	}
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	request, _ := http.NewRequest("GET", server.URL+"/api/v1/entities/stream?entity_ids=drone-001", nil)
	request.Host = "localhost:18090"
	request.AddCookie(cookie)
	client := &http.Client{Timeout: 10 * time.Second}
	response, e := client.Do(request)
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	line, e := reader.ReadString('\n')
	if e != nil || line != "event: update\n" {
		t.Fatalf("SSE frame %q %v", line, e)
	}
	data, e := reader.ReadString('\n')
	if e != nil || !strings.Contains(data, "SNAPSHOT_END") {
		t.Fatalf("sync marker %q %v", data, e)
	}
	logout := httptest.NewRequest("DELETE", "http://localhost:18090/api/session", nil)
	logout.AddCookie(cookie)
	logout.Header.Set("Origin", "http://localhost:18090")
	logout.Header.Set("X-CSRF-Token", csrf)
	s.Handler().ServeHTTP(httptest.NewRecorder(), logout)
	select {
	case <-probe.cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("logout did not cancel upstream subscription")
	}
}
