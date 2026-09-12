package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	taskv1 "example.com/meshops-course/gen/task/v1"
	"example.com/meshops-course/internal/platform"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type taskProbe struct {
	taskv1.TaskServiceClient
	token string
	calls int
}

func (p *taskProbe) ListTasks(ctx context.Context, r *taskv1.ListTasksRequest, _ ...grpc.CallOption) (*taskv1.ListTasksResponse, error) {
	md, _ := metadata.FromOutgoingContext(ctx)
	p.token = strings.Join(md.Get("authorization"), ",")
	p.calls++
	return &taskv1.ListTasksResponse{TotalCount: 7}, nil
}
func fixture(t *testing.T) (*Server, *taskProbe) {
	t.Helper()
	r := &platform.Registry{Principals: map[string]platform.Principal{}, Credentials: map[string]string{}, Bindings: map[string]platform.Binding{}}
	for _, role := range []string{"operator", "admin"} {
		token := strings.Repeat(role, 12)
		p := platform.Principal{ID: role, TenantID: "demo_tenant", Role: role}
		r.Principals[platform.Hash([]byte(token))] = p
		r.Credentials[platform.Key(p.TenantID, p.ID)] = token
	}
	r.Bindings[platform.Key("demo_tenant", "drone-001")] = platform.Binding{TenantID: "demo_tenant", EntityID: "drone-001", Type: "drone"}
	r.Bindings[platform.Key("other", "secret")] = platform.Binding{TenantID: "other", EntityID: "secret", Type: "person"}
	probe := &taskProbe{}
	s, e := New(Config{Registry: r, TenantID: "demo_tenant", Task: probe, AccessCodes: map[string]string{"operator": strings.Repeat("o", 32), "admin": strings.Repeat("a", 32)}, AllowedOrigins: []string{"http://localhost:18090"}, SessionTTL: time.Hour})
	if e != nil {
		t.Fatal(e)
	}
	return s, probe
}
func request(s *Server, method, path, body string, cookie *http.Cookie, csrf, origin string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://localhost:18090"+path, bytes.NewBufferString(body))
	r.Header.Set("Content-Type", "application/json")
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if csrf != "" {
		r.Header.Set("X-CSRF-Token", csrf)
	}
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}
func login(t *testing.T, s *Server, role string) (*http.Cookie, string) {
	t.Helper()
	code := strings.Repeat(string(role[0]), 32)
	w := request(s, "POST", "/api/session", `{"role":"`+role+`","accessCode":"`+code+`"}`, nil, "", "http://localhost:18090")
	if w.Code != 200 {
		t.Fatalf("login %d %s", w.Code, w.Body.String())
	}
	var data struct {
		CSRFToken string `json:"csrfToken"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &data); e != nil {
		t.Fatal(e)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || data.CSRFToken == "" {
		t.Fatal("session cookie/CSRF missing")
	}
	return cookies[0], data.CSRFToken
}
func TestSessionBoundary(t *testing.T) {
	s, probe := fixture(t)
	hostile := httptest.NewRequest("GET", "http://evil.invalid/api/v1/tasks", nil)
	hostileResponse := httptest.NewRecorder()
	s.Handler().ServeHTTP(hostileResponse, hostile)
	if hostileResponse.Code != http.StatusForbidden || probe.calls != 0 {
		t.Fatal("untrusted Host reached session or RPC handling")
	}
	if w := request(s, "GET", "/api/v1/tasks", "", nil, "", ""); w.Code != 401 {
		t.Fatalf("unauthenticated status %d", w.Code)
	}
	if w := request(s, "POST", "/api/session", `{"role":"operator","accessCode":"`+strings.Repeat("o", 32)+`"}`, nil, "", "https://evil.invalid"); w.Code != 403 {
		t.Fatalf("foreign login status %d", w.Code)
	}
	c, csrf := login(t, s, "operator")
	if w := request(s, "GET", "/api/v1/tasks", "", c, "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"totalCount":7`) {
		t.Fatalf("query %d %s", w.Code, w.Body.String())
	}
	if probe.calls != 1 || probe.token != "Bearer "+strings.Repeat("operator", 12) {
		t.Fatal("untrusted upstream principal")
	}
	if w := request(s, "POST", "/api/v1/tasks/x/retry", `{"reason":"retry"}`, c, csrf, "http://localhost:18090"); w.Code != 403 {
		t.Fatalf("operator admin status %d", w.Code)
	}
	if w := request(s, "POST", "/api/v1/tasks", `{}`, c, "", "http://localhost:18090"); w.Code != 403 {
		t.Fatalf("missing CSRF %d", w.Code)
	}
	if w := request(s, "POST", "/api/v1/tasks", `{}`, c, csrf, "https://evil.invalid"); w.Code != 403 {
		t.Fatalf("foreign mutation %d", w.Code)
	}
	if w := request(s, "GET", "/api/v1/entities?tenant=other", "", c, "", ""); w.Code != 200 || strings.Contains(w.Body.String(), "secret") || !strings.Contains(w.Body.String(), "drone-001") {
		t.Fatalf("inventory isolation %d %s", w.Code, w.Body.String())
	}
	if w := request(s, "POST", "/api/v1/tasks/report", `{}`, c, csrf, "http://localhost:18090"); w.Code != 404 && w.Code != 405 {
		t.Fatalf("report route exposed: %d", w.Code)
	}
	if w := request(s, "DELETE", "/api/session", "", c, csrf, "http://localhost:18090"); w.Code != 204 {
		t.Fatalf("logout %d", w.Code)
	}
	if w := request(s, "GET", "/api/session", "", c, "", ""); w.Code != 401 {
		t.Fatalf("session survived logout %d", w.Code)
	}
}
func TestSessionExpiresAndDoesNotReturnMachineToken(t *testing.T) {
	s, _ := fixture(t)
	now := time.Now()
	s.cfg.Now = func() time.Time { return now }
	c, _ := login(t, s, "admin")
	w := request(s, "GET", "/api/session", "", c, "", "")
	if strings.Contains(w.Body.String(), strings.Repeat("admin", 12)) {
		t.Fatal("machine token leaked")
	}
	now = now.Add(2 * time.Hour)
	if w := request(s, "GET", "/api/session", "", c, "", ""); w.Code != 401 {
		t.Fatalf("expired status %d", w.Code)
	}
}
