package platform

import (
	"context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConfigDefaultsAndPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.yaml")
	if e := os.WriteFile(path, []byte("Name: test\nListenOn: 127.0.0.1:50052\nMode: test\nMeshOps:\n  Manifest: manifests/demo.yaml\n"), 0600); e != nil {
		t.Fatal(e)
	}
	t.Setenv("MESHOPS_KAFKA_BROKERS", "localhost:19092")
	for _, name := range []string{"MESHOPS_ENTITY_ENDPOINT", "MESHOPS_INGEST_ENDPOINT", "MESHOPS_TASK_ENDPOINT", "MESHOPS_DISPATCHER_ENDPOINT"} {
		t.Setenv(name, "")
	}
	c, e := LoadConfig(path)
	if e != nil {
		t.Fatal(e)
	}
	if c.MeshOps.Manifest != filepath.Join(dir, "manifests/demo.yaml") || c.MeshOps.KafkaBrokers[0] != "localhost:19092" || c.MeshOps.BatchSize != 100 {
		t.Fatal(c.MeshOps)
	}
	if c.MeshOps.EntityEndpoint != "127.0.0.1:50052" || c.MeshOps.IngestEndpoint != "127.0.0.1:50051" || c.MeshOps.TaskEndpoint != "127.0.0.1:50053" || c.MeshOps.DispatcherEndpoint != "127.0.0.1:50054" {
		t.Fatal("default clients must address the same IPv4 loopback as ListenOn")
	}
}
func TestAuthBoundary(t *testing.T) {
	token := strings.Repeat("a", 32)
	p := Principal{ID: "reader", TenantID: "a", Role: "operator"}
	r := &Registry{Principals: map[string]Principal{Hash([]byte(token)): p}, Credentials: map[string]string{Key("a", "reader"): token}}
	call := r.Unary()
	handler := func(ctx context.Context, _ any) (any, error) { return Identity(ctx), nil }
	_, e := call(context.Background(), nil, &grpc.UnaryServerInfo{FullMethod: "/meshops.entity.v1.EntityService/GetSnapshot"}, handler)
	if status.Code(e) != codes.Unauthenticated {
		t.Fatal(e)
	}
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token))
	got, e := call(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/meshops.entity.v1.EntityService/GetSnapshot"}, handler)
	if e != nil || got.(Principal).TenantID != "a" {
		t.Fatal(got, e)
	}
	_, e = call(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/meshops.task.v1.TaskService/ReportTaskStatus"}, handler)
	if status.Code(e) != codes.PermissionDenied {
		t.Fatal(e)
	}
}

// 教学传输未启用 TLS，配置必须防止 Bearer 令牌传出本机。
func TestPlaintextRPCRejectsNonLoopback(t *testing.T) {
	for _, endpoint := range []string{"0.0.0.0:50052", "192.0.2.1:50052", "[::]:50052", "example.com:50052", "dns:///127.0.0.1:50052", "127.0.0.1:0", "127.0.0.1:65536"} {
		t.Run("dial_"+endpoint, func(t *testing.T) {
			conn, err := Dial(endpoint)
			if conn != nil {
				conn.Close()
			}
			if err == nil {
				t.Fatalf("plaintext connection accepted %q", endpoint)
			}
		})
	}
	for _, endpoint := range []string{"127.0.0.1:50052", "[::1]:50052"} {
		conn, err := Dial(endpoint)
		if err != nil {
			t.Fatal(err)
		}
		conn.Close()
	}
	for _, name := range []string{"MESHOPS_ENTITY_ENDPOINT", "MESHOPS_INGEST_ENDPOINT", "MESHOPS_TASK_ENDPOINT", "MESHOPS_DISPATCHER_ENDPOINT"} {
		t.Setenv(name, "")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "server.yaml")
	for _, address := range []string{"0.0.0.0:50052", "[::]:50052", "192.0.2.1:50052"} {
		if err := os.WriteFile(path, []byte("Name: test\nListenOn: '"+address+"'\nMode: test\nMeshOps:\n  Manifest: fixture.yaml\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(path); err == nil {
			t.Fatalf("plaintext listener accepted %q", address)
		}
	}
	if err := os.WriteFile(path, []byte("Name: test\nListenOn: 127.0.0.1:50052\nMode: test\nMeshOps:\n  Manifest: fixture.yaml\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MESHOPS_TASK_ENDPOINT", "192.0.2.1:50053")
	if _, err := LoadConfig(path); err == nil {
		t.Fatal("external service endpoint accepted from environment")
	}
}
func TestCursorScopeAndExpiry(t *testing.T) {
	now := time.Now()
	key := []byte(strings.Repeat("k", 32))
	c := Cursor{Kind: "task", Tenant: "a", Filter: "filter", Expires: now.Add(time.Minute)}
	token, e := SignCursor(c, key)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = ReadCursor(token, key, "task", "a", "filter", now); e != nil {
		t.Fatal(e)
	}
	if _, e = ReadCursor(token, key, "task", "b", "filter", now); e == nil {
		t.Fatal("cross-tenant cursor accepted")
	}
	if _, e = ReadCursor(token, key, "task", "a", "filter", now.Add(2*time.Minute)); e == nil {
		t.Fatal("expired cursor accepted")
	}
}

func TestInvalidConfigurationAndCredentialsFailWithoutLeaking(t *testing.T) {
	dir := t.TempDir()
	for _, item := range []struct{ name, fields string }{
		{"missing manifest", ""}, {"negative capacity", "  Manifest: fixture.yaml\n  BatchSize: -1\n"}, {"oversize batch", "  Manifest: fixture.yaml\n  BatchSize: 101\n"},
	} {
		t.Run(item.name, func(t *testing.T) {
			path := filepath.Join(dir, "service.yaml")
			if err := os.WriteFile(path, []byte("Name: test\nListenOn: 127.0.0.1:50051\nMode: test\nMeshOps:\n"+item.fields), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadConfig(path); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
	manifest := "tenant_id: test\ntask_catalog:\n  - task_type: inspect\nactors:\n  - id: op\n    role: operator\n    credential_env: TEST_OPERATOR\n  - id: adm\n    role: admin\n    credential_env: TEST_ADMIN\n  - id: task\n    role: task_service\n    credential_env: TEST_TASK\n  - id: dispatcher\n    role: dispatcher_service\n    credential_env: TEST_DISPATCHER\n"
	path := filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(path, []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	token := strings.Repeat("s", 40)
	for _, name := range []string{"TEST_OPERATOR", "TEST_ADMIN", "TEST_TASK", "TEST_DISPATCHER"} {
		t.Setenv(name, token+name)
	}
	if _, err := LoadRegistry(path); err != nil {
		t.Fatal("valid fixture", err)
	}
	t.Run("missing token", func(t *testing.T) {
		t.Setenv("TEST_OPERATOR", "")
		_, err := LoadRegistry(path)
		if err == nil || !strings.Contains(err.Error(), "TEST_OPERATOR") || strings.Contains(err.Error(), token) {
			t.Fatal("missing credential was accepted or leaked", err)
		}
	})
	t.Run("duplicate token", func(t *testing.T) {
		t.Setenv("TEST_ADMIN", token+"TEST_OPERATOR")
		_, err := LoadRegistry(path)
		if err == nil || !strings.Contains(err.Error(), "duplicate credential") || strings.Contains(err.Error(), token) {
			t.Fatal("duplicate credential was accepted or leaked", err)
		}
	})
}
