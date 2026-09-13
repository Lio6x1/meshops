package web

import (
	"context"
	"encoding/json"
	"errors"
	"example.com/meshops-course/internal/accounts"
	"example.com/meshops-course/internal/platform"
	"io"
	"net/http"
	"strconv"
	"time"
)

type AccountService interface {
	Login(context.Context, string, string, string, time.Duration) (accounts.Account, string, error)
	CheckSession(context.Context, string) (accounts.Account, error)
	Logout(context.Context, string) error
	List(context.Context, platform.Principal, int, string) ([]accounts.Account, string, error)
	Create(context.Context, platform.Principal, string, string, string) (accounts.Account, error)
	SetEnabled(context.Context, platform.Principal, string, bool) (accounts.Account, error)
	ResetPassword(context.Context, platform.Principal, string, string) error
	ChangePassword(context.Context, platform.Principal, string, string) error
}

func accountError(w http.ResponseWriter, e error) {
	code := 503
	message := "account service unavailable"
	switch {
	case errors.Is(e, accounts.ErrInvalid):
		code = 400
		message = e.Error()
	case errors.Is(e, accounts.ErrCredentials):
		code = 401
		message = "invalid credentials or expired session"
	case errors.Is(e, accounts.ErrForbidden):
		code = 403
		message = e.Error()
	case errors.Is(e, accounts.ErrNotFound):
		code = 404
		message = e.Error()
	case errors.Is(e, accounts.ErrConflict):
		code = 409
		message = e.Error()
	case errors.Is(e, accounts.ErrBusy):
		code = 429
		message = e.Error()
	}
	webError(w, code, message)
}
func decodeAccount(w http.ResponseWriter, r *http.Request, v any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		webError(w, 400, "invalid account JSON")
		return false
	}
	var more any
	if d.Decode(&more) != io.EOF {
		webError(w, 400, "one JSON object required")
		return false
	}
	return true
}
func clearCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Path: "/api", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil, MaxAge: -1})
}
func (s *Server) removeSession(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.sessions[id]; p != nil {
		delete(s.sessions, id)
		close(p.done)
	}
}
func (s *Server) revokeUser(tenant, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, p := range s.sessions {
		if p.tenant == tenant && p.actor == id {
			delete(s.sessions, k)
			close(p.done)
		}
	}
}
func (s *Server) accountLoginHTTP(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeAccount(w, r, &b) {
		return
	}
	// 哈希与数据库操作不持有全局会话锁，不阻塞其他用户查询。
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	a, token, e := s.cfg.Accounts.Login(ctx, s.cfg.TenantID, b.Username, b.Password, s.cfg.SessionTTL)
	if e != nil {
		accountError(w, e)
		return
	}
	revoke := func() {
		cleanup, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_ = s.cfg.Accounts.Logout(cleanup, token)
	}
	id, e := secret()
	if e != nil {
		revoke()
		accountError(w, e)
		return
	}
	csrf, e := secret()
	if e != nil {
		revoke()
		accountError(w, e)
		return
	}
	now := s.cfg.Now()
	p := &session{authVersion: a.Principal().AuthVersion, id: id, csrf: csrf, role: a.Role, actor: a.ID, tenant: a.TenantID, username: a.Username, displayName: a.DisplayName, mustChange: a.MustChangePassword, token: token, expires: now.Add(s.cfg.SessionTTL), done: make(chan struct{})}
	// 先撤销旧内部凭证，再替换浏览器 Cookie，避免重新登录遗留有效令牌。
	if old := s.authenticate(r); old != nil {
		if e = s.cfg.Accounts.Logout(ctx, old.token); e != nil {
			revoke()
			accountError(w, e)
			return
		}
		s.removeSession(old.id)
	}
	s.mu.Lock()
	s.purgeLocked(now)
	if len(s.sessions) >= 256 {
		s.mu.Unlock()
		revoke()
		webError(w, 429, "session capacity reached")
		return
	}
	s.sessions[id] = p
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: id, Path: "/api", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil, MaxAge: int(s.cfg.SessionTTL.Seconds())})
	writeJSON(w, 200, s.sessionResponse(p))
}
func sessionPrincipal(p *session) platform.Principal {
	return platform.Principal{ID: p.actor, TenantID: p.tenant, Role: p.role, AuthVersion: p.authVersion, SessionHash: platform.Hash([]byte(p.token))}
}
func (s *Server) passwordHTTP(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Accounts == nil {
		webError(w, 404, "accounts unavailable")
		return
	}
	p := r.Context().Value(sessionKey{}).(*session)
	var b struct {
		Current string `json:"currentPassword"`
		New     string `json:"newPassword"`
	}
	if !decodeAccount(w, r, &b) {
		return
	}
	if e := s.cfg.Accounts.ChangePassword(r.Context(), sessionPrincipal(p), b.Current, b.New); e != nil {
		accountError(w, e)
		return
	}
	s.revokeUser(p.tenant, p.actor)
	clearCookie(w, r)
	w.WriteHeader(204)
}
func (s *Server) accountsHTTP(w http.ResponseWriter, r *http.Request) {
	if s.cfg.Accounts == nil {
		webError(w, 404, "accounts unavailable")
		return
	}
	p := r.Context().Value(sessionKey{}).(*session)
	if p.role != "admin" {
		webError(w, 403, "administrator required")
		return
	}
	principal := sessionPrincipal(p)
	id := r.PathValue("id")
	switch {
	case r.Method == "GET":
		limit := 20
		if raw := r.URL.Query().Get("limit"); raw != "" {
			v, e := strconv.Atoi(raw)
			if e != nil {
				accountError(w, accounts.ErrInvalid)
				return
			}
			limit = v
		}
		out, next, e := s.cfg.Accounts.List(r.Context(), principal, limit, r.URL.Query().Get("after"))
		if e != nil {
			accountError(w, e)
			return
		}
		writeJSON(w, 200, map[string]any{"accounts": out, "nextCursor": next})
	case r.Method == "PATCH":
		var b struct {
			Enabled *bool `json:"enabled"`
		}
		if !decodeAccount(w, r, &b) {
			return
		}
		if b.Enabled == nil {
			accountError(w, accounts.ErrInvalid)
			return
		}
		a, e := s.cfg.Accounts.SetEnabled(r.Context(), principal, id, *b.Enabled)
		if e != nil {
			accountError(w, e)
			return
		}
		s.revokeUser(p.tenant, id)
		writeJSON(w, 200, a)
	case id != "":
		var b struct {
			Password string `json:"password"`
		}
		if !decodeAccount(w, r, &b) {
			return
		}
		if e := s.cfg.Accounts.ResetPassword(r.Context(), principal, id, b.Password); e != nil {
			accountError(w, e)
			return
		}
		s.revokeUser(p.tenant, id)
		w.WriteHeader(204)
	default:
		var b struct {
			Username    string `json:"username"`
			DisplayName string `json:"displayName"`
			Password    string `json:"password"`
		}
		if !decodeAccount(w, r, &b) {
			return
		}
		a, e := s.cfg.Accounts.Create(r.Context(), principal, b.Username, b.DisplayName, b.Password)
		if e != nil {
			accountError(w, e)
			return
		}
		writeJSON(w, 201, a)
	}
}
