package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"sync/atomic"
	"time"

	"example.com/meshops-course/internal/platform"
)

const cookieName = "meshops_session"

type session struct {
	id, csrf, role, actor, tenant, token string
	expires                              time.Time
	streams                              atomic.Int32
	// Closing done also cancels streams already running when the user logs out.
	done chan struct{}
}

func equal(a, b string) bool {
	return len(a) > 0 && len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
func secret() (string, error) {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
func webError(w http.ResponseWriter, httpCode int, message string) {
	code := 13
	switch httpCode {
	case 400:
		code = 3
	case 401:
		code = 16
	case 403:
		code = 7
	case 404:
		code = 5
	case 429:
		code = 8
	case 503:
		code = 14
	}
	writeJSON(w, httpCode, map[string]any{"code": code, "message": message})
}
func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
func (s *Server) purgeLocked(now time.Time) {
	for k, p := range s.sessions {
		if !now.Before(p.expires) {
			delete(s.sessions, k)
			close(p.done)
		}
	}
}
func (s *Server) authenticate(r *http.Request) *session {
	c, e := r.Cookie(cookieName)
	if e != nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgeLocked(s.cfg.Now())
	return s.sessions[c.Value]
}
func (s *Server) sessionResponse(p *session) map[string]any {
	return map[string]any{"role": p.role, "tenantId": p.tenant, "actorId": p.actor, "csrfToken": p.csrf, "expiresAt": p.expires.UTC().Format(time.RFC3339), "serverTime": s.cfg.Now().UTC().Format(time.RFC3339Nano)}
}

func (s *Server) sessionHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		now := s.cfg.Now()
		s.mu.Lock()
		if now.Sub(s.loginWindow) >= time.Minute {
			s.loginWindow = now
			s.loginAttempts = 0
		}
		s.loginAttempts++
		limited := s.loginAttempts > 30
		s.mu.Unlock()
		if limited {
			w.Header().Set("Retry-After", "60")
			webError(w, 429, "too many login attempts")
			return
		}
		var body struct {
			Role       string `json:"role"`
			AccessCode string `json:"accessCode"`
		}
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		dec.DisallowUnknownFields()
		if e := dec.Decode(&body); e != nil {
			webError(w, 400, "invalid login JSON")
			return
		}
		var extra any
		if dec.Decode(&extra) != io.EOF {
			webError(w, 400, "one JSON object required")
			return
		}
		if (body.Role != "operator" && body.Role != "admin") || !equal(body.AccessCode, s.cfg.AccessCodes[body.Role]) {
			webError(w, 401, "invalid access code or role")
			return
		}
		token, e := s.cfg.Registry.ServiceToken(s.cfg.TenantID, body.Role)
		if e != nil {
			webError(w, 503, "actor unavailable")
			return
		}
		principal, e := s.cfg.Registry.Authenticate(token)
		if e != nil {
			webError(w, 503, "actor unavailable")
			return
		}
		id, e := secret()
		if e != nil {
			webError(w, 503, "session unavailable")
			return
		}
		csrf, e := secret()
		if e != nil {
			webError(w, 503, "session unavailable")
			return
		}
		p := &session{id: id, csrf: csrf, role: principal.Role, actor: principal.ID, tenant: principal.TenantID, token: token, expires: now.Add(s.cfg.SessionTTL), done: make(chan struct{})}
		s.mu.Lock()
		s.purgeLocked(now)
		if old, e := r.Cookie(cookieName); e == nil {
			if v := s.sessions[old.Value]; v != nil {
				delete(s.sessions, old.Value)
				close(v.done)
			}
		}
		if len(s.sessions) >= 256 {
			s.mu.Unlock()
			webError(w, 429, "session capacity reached")
			return
		}
		s.sessions[id] = p
		s.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: cookieName, Value: id, Path: "/api", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil, MaxAge: int(s.cfg.SessionTTL.Seconds())})
		writeJSON(w, 200, s.sessionResponse(p))
		return
	}
	p, _ := r.Context().Value(sessionKey{}).(*session)
	switch r.Method {
	case "GET":
		writeJSON(w, 200, s.sessionResponse(p))
	case "DELETE":
		s.mu.Lock()
		if old := s.sessions[p.id]; old != nil {
			delete(s.sessions, p.id)
			close(old.done)
		}
		s.mu.Unlock()
		http.SetCookie(w, &http.Cookie{Name: cookieName, Path: "/api", HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil, MaxAge: -1})
		w.WriteHeader(204)
	default:
		w.Header().Set("Allow", "GET, POST, DELETE")
		webError(w, 405, "method not allowed")
	}
}
func (s *Server) inventory(w http.ResponseWriter, r *http.Request) {
	p := r.Context().Value(sessionKey{}).(*session)
	active, err := s.activeSimulationEntities(r.Context())
	if err != nil {
		webError(w, 503, "无法读取当前模拟场景数量，请稍后刷新")
		return
	}
	type item struct {
		EntityID string   `json:"entityId"`
		Type     string   `json:"entityType"`
		Executor string   `json:"executorId"`
		Tasks    []string `json:"supportedTasks"`
	}
	entries := []item{}
	for _, b := range s.cfg.Registry.Bindings {
		if b.TenantID == p.tenant {
			if enabled, controlled := active[b.EntityID]; controlled && !enabled {
				continue
			}
			tasks := append([]string{}, b.Tasks...)
			entries = append(entries, item{b.EntityID, b.Type, b.ExecutorID, tasks})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].EntityID < entries[j].EntityID })
	writeJSON(w, 200, map[string]any{"entities": entries, "serverTime": s.cfg.Now().UTC().Format(time.RFC3339Nano)})
}
func (s *Server) allowedEntity(p *session, id string) bool {
	_, ok := s.cfg.Registry.Bindings[platform.Key(p.tenant, id)]
	return ok
}
