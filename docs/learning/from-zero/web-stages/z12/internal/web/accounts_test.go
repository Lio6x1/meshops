package web

import (
	"context"
	"encoding/json"
	"errors"
	"example.com/meshops-course/internal/accounts"
	"example.com/meshops-course/internal/platform"
	"net/http"
	"strings"
	"testing"
	"time"
)

type accountProbe struct {
	AccountService
	account accounts.Account
	token   string
	revoked bool
	changed bool
	failure error
}

type blockedSessionProbe struct {
	AccountService
	bounded bool
}

func (p *blockedSessionProbe) CheckSession(ctx context.Context, _ string) (accounts.Account, error) {
	deadline, ok := ctx.Deadline()
	if !ok || time.Until(deadline) > 3*time.Second {
		return accounts.Account{}, errors.New("session check has no bounded deadline")
	}
	p.bounded = true
	// 模拟连接池耗尽：直到调用者取消才返回，不能依赖数据库读写超时。
	<-ctx.Done()
	return accounts.Account{}, ctx.Err()
}

func TestAccountSessionPrecheckHasBoundedDeadline(t *testing.T) {
	for _, path := range []string{"/api/v1/tasks", "/api/v1/entities/stream"} {
		t.Run(path, func(t *testing.T) {
			s, task, p := accountFixture(t, false)
			cookie, _ := accountLogin(t, s)
			blocked := &blockedSessionProbe{AccountService: p}
			s.cfg.Accounts = blocked
			w := request(s, "GET", path, "", cookie, "", "")
			if !blocked.bounded || w.Code != 503 || task.calls != 0 {
				t.Fatalf("bounded=%v status=%d upstream calls=%d", blocked.bounded, w.Code, task.calls)
			}
		})
	}
}

func (p *accountProbe) Login(_ context.Context, tenant, u, password string, _ time.Duration) (accounts.Account, string, error) {
	if tenant != "demo_tenant" || u != "alice" || password != "correct-password-123" {
		return accounts.Account{}, "", accounts.ErrCredentials
	}
	p.revoked = false
	return p.account, p.token, nil
}
func (p *accountProbe) CheckSession(_ context.Context, token string) (accounts.Account, error) {
	if p.failure != nil {
		return accounts.Account{}, p.failure
	}
	if token != p.token || p.revoked {
		return accounts.Account{}, accounts.ErrCredentials
	}
	return p.account, nil
}

func TestAccountDatabaseOutageFailsClosed(t *testing.T) {
	s, task, p := accountFixture(t, false)
	cookie, _ := accountLogin(t, s)
	p.failure = errors.New("private database diagnostic")
	w := request(s, "GET", "/api/v1/tasks", "", cookie, "", "")
	if w.Code != 503 || task.calls != 0 || strings.Contains(w.Body.String(), "private") {
		t.Fatal(w.Code, w.Body.String())
	}
	p.failure = nil
	if w = request(s, "GET", "/api/v1/tasks", "", cookie, "", ""); w.Code != 401 || task.calls != 0 {
		t.Fatal("failed authentication retained session", w.Code)
	}
}
func (p *accountProbe) Logout(context.Context, string) error { p.revoked = true; return nil }
func (p *accountProbe) ChangePassword(_ context.Context, pn platform.Principal, current, new string) error {
	if pn.ID != "personal-alice" || current != "correct-password-123" || new != "replacement-password-123" {
		return accounts.ErrCredentials
	}
	p.changed = true
	p.revoked = true
	return nil
}
func (p *accountProbe) SetEnabled(_ context.Context, pn platform.Principal, id string, enabled bool) (accounts.Account, error) {
	if pn.Role != "admin" {
		return accounts.Account{}, accounts.ErrForbidden
	}
	if id != "personal-alice" {
		return accounts.Account{}, accounts.ErrNotFound
	}
	p.revoked = true
	a := p.account
	a.Enabled = enabled
	return a, nil
}
func accountFixture(t *testing.T, must bool) (*Server, *taskProbe, *accountProbe) {
	base, task := fixture(t)
	p := &accountProbe{account: accounts.Account{ID: "personal-alice", TenantID: "demo_tenant", Username: "alice", DisplayName: "Alice", Role: "operator", Enabled: true, MustChangePassword: must}, token: strings.Repeat("s", 43)}
	cfg := base.cfg
	cfg.Accounts = p
	cfg.AccessCodes = nil
	s, e := New(cfg)
	if e != nil {
		t.Fatal(e)
	}
	return s, task, p
}
func accountLogin(t *testing.T, s *Server) (*http.Cookie, string) {
	t.Helper()
	w := request(s, "POST", "/api/session", `{"username":"alice","password":"correct-password-123"}`, nil, "", "http://localhost:18090")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var v map[string]any
	if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil {
		t.Fatal(e)
	}
	if v["actorId"] != "personal-alice" || v["username"] != "alice" || strings.Contains(w.Body.String(), strings.Repeat("s", 43)) {
		t.Fatal(w.Body.String())
	}
	c := w.Result().Cookies()[0]
	if c.Value == strings.Repeat("s", 43) {
		t.Fatal("internal token exposed as cookie")
	}
	return c, v["csrfToken"].(string)
}
func TestAccountMustChangeAndRevocation(t *testing.T) {
	s, task, p := accountFixture(t, true)
	c, csrf := accountLogin(t, s)
	old := s.sessions[c.Value]
	if w := request(s, "GET", "/api/v1/tasks", "", c, "", ""); w.Code != 403 || task.calls != 0 {
		t.Fatal(w.Code)
	}
	if w := request(s, "GET", "/api/session", "", c, "", ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := request(s, "POST", "/api/account/password", `{"currentPassword":"correct-password-123","newPassword":"replacement-password-123"}`, c, "", "http://localhost:18090"); w.Code != 403 || p.changed {
		t.Fatal("CSRF bypass")
	}
	w := request(s, "POST", "/api/account/password", `{"currentPassword":"correct-password-123","newPassword":"replacement-password-123"}`, c, csrf, "http://localhost:18090")
	if w.Code != 204 || !p.changed {
		t.Fatal(w.Code, w.Body.String())
	}
	select {
	case <-old.done:
	default:
		t.Fatal("SSE not cancelled")
	}
	if w = request(s, "GET", "/api/session", "", c, "", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
}
func TestAccountRejectsRoleSpoofAndUsesPersonalToken(t *testing.T) {
	s, task, p := accountFixture(t, false)
	if w := request(s, "POST", "/api/session", `{"username":"alice","password":"correct-password-123","role":"admin"}`, nil, "", "http://localhost:18090"); w.Code != 400 {
		t.Fatal(w.Code)
	}
	c, _ := accountLogin(t, s)
	if w := request(s, "GET", "/api/v1/tasks", "", c, "", ""); w.Code != 200 || task.token != "Bearer "+p.token {
		t.Fatal(w.Code, task.token)
	}
	if w := request(s, "GET", "/api/accounts", "", c, "", ""); w.Code != 403 {
		t.Fatal(w.Code)
	}
	p.revoked = true
	if w := request(s, "GET", "/api/v1/tasks", "", c, "", ""); w.Code != 401 || task.calls != 1 {
		t.Fatal(w.Code)
	}
}
