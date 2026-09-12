// Package web adapts browser sessions to the existing, authenticated RPC APIs.
package web

import (
	"context"
	"errors"
	dispatcherv1 "example.com/meshops-course/gen/dispatcher/v1"
	entityv1 "example.com/meshops-course/gen/entity/v1"
	searchv1 "example.com/meshops-course/gen/search/v1"
	taskv1 "example.com/meshops-course/gen/task/v1"
	"example.com/meshops-course/internal/platform"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Config struct {
	Simulation     SimulationControl
	Registry       *platform.Registry
	TenantID       string
	Entity         entityv1.EntityServiceClient
	Task           taskv1.TaskServiceClient
	Dispatcher     dispatcherv1.DispatcherServiceClient
	Search         searchv1.SearchServiceClient
	AccessCodes    map[string]string
	AllowedOrigins []string
	SessionTTL     time.Duration
	Now            func() time.Time
}

type Server struct {
	cfg           Config
	mux           http.Handler
	origins       map[string]bool
	hosts         map[string]bool
	mu            sync.Mutex
	sessions      map[string]*session
	loginWindow   time.Time
	loginAttempts int
	streams       atomic.Int32
}
type sessionKey struct{}

func New(cfg Config) (*Server, error) {
	if cfg.Registry == nil || cfg.TenantID == "" {
		return nil, errors.New("trusted registry and tenant required")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.SessionTTL == 0 {
		cfg.SessionTTL = 8 * time.Hour
	}
	if cfg.SessionTTL < time.Minute || cfg.SessionTTL > 24*time.Hour {
		return nil, errors.New("session TTL outside 1m..24h")
	}
	for _, role := range []string{"operator", "admin"} {
		if len(cfg.AccessCodes[role]) < 32 {
			return nil, errors.New("independent random browser access codes required")
		}
		if _, e := cfg.Registry.ServiceToken(cfg.TenantID, role); e != nil {
			return nil, e
		}
	}
	if cfg.AccessCodes["operator"] == cfg.AccessCodes["admin"] {
		return nil, errors.New("browser role codes must differ")
	}
	s := &Server{cfg: cfg, sessions: map[string]*session{}, origins: map[string]bool{}, hosts: map[string]bool{}}
	for _, origin := range cfg.AllowedOrigins {
		u, e := url.Parse(origin)
		if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
			return nil, errors.New("invalid browser origin")
		}
		s.origins[origin] = true
		s.hosts[u.Host] = true
	}
	if len(s.origins) == 0 {
		return nil, errors.New("browser origin allowlist required")
	}
	rpc := runtime.NewServeMux(runtime.WithRoutingErrorHandler(func(_ context.Context, _ *runtime.ServeMux, _ runtime.Marshaler, w http.ResponseWriter, _ *http.Request, code int) {
		webError(w, code, "HTTP route or method unavailable")
	}), runtime.WithIncomingHeaderMatcher(func(string) (string, bool) { return "", false }), runtime.WithMetadata(func(ctx context.Context, _ *http.Request) metadata.MD {
		p, _ := ctx.Value(sessionKey{}).(*session)
		if p == nil {
			return nil
		}
		return metadata.Pairs("authorization", "Bearer "+p.token)
	}), runtime.WithErrorHandler(func(ctx context.Context, m *runtime.ServeMux, mar runtime.Marshaler, w http.ResponseWriter, r *http.Request, err error) {
		code := status.Code(err)
		if code == codes.Internal || code == codes.Unknown || code == codes.Unavailable {
			err = status.Error(code, "backend unavailable; inspect service logs")
		}
		runtime.DefaultHTTPErrorHandler(ctx, m, mar, w, r, err)
	}))
	ctx := context.Background()
	if e := entityv1.RegisterEntityServiceHandlerClient(ctx, rpc, cfg.Entity); e != nil {
		return nil, e
	}
	if e := taskv1.RegisterTaskServiceHandlerClient(ctx, rpc, cfg.Task); e != nil {
		return nil, e
	}
	if e := dispatcherv1.RegisterDispatcherServiceHandlerClient(ctx, rpc, cfg.Dispatcher); e != nil {
		return nil, e
	}
	if e := searchv1.RegisterSearchServiceHandlerClient(ctx, rpc, cfg.Search); e != nil {
		return nil, e
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/session", s.sessionHTTP)
	mux.HandleFunc("GET /api/v1/entities", s.inventory)
	mux.HandleFunc("GET /api/v1/entities/stream", s.stream)
	mux.HandleFunc("GET /api/v1/simulation", s.simulationHTTP)
	mux.HandleFunc("PUT /api/v1/simulation/{source}", s.simulationHTTP)
	mux.Handle("/api/", rpc)
	s.mux = mux
	return s, nil
}

// Handler authenticates once, strips client-controlled RPC metadata and keeps the
// selected actor identity in the server context. Generated handlers only translate.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if r.URL.Path == "/livez" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if !s.hosts[r.Host] {
			webError(w, 403, "untrusted host")
			return
		}
		origin := r.Header.Get("Origin")
		if origin != "" && !s.origins[origin] {
			webError(w, 403, "untrusted origin")
			return
		}
		if r.URL.Path == "/api/session" && r.Method == "POST" {
			if !s.origins[origin] {
				webError(w, 403, "trusted origin required")
				return
			}
			s.sessionHTTP(w, r)
			return
		}
		p := s.authenticate(r)
		if p == nil {
			webError(w, 401, "browser session required")
			return
		}
		if r.Method != "GET" && r.Method != "HEAD" {
			if !s.origins[origin] || !equal(r.Header.Get("X-CSRF-Token"), p.csrf) {
				webError(w, 403, "CSRF validation failed")
				return
			}
		}
		if p.role != "admin" && (r.URL.Path == "/api/v1/dispatcher/status" || strings.HasSuffix(r.URL.Path, "/retry")) {
			webError(w, 403, "administrator required")
			return
		}
		ctx := context.WithValue(r.Context(), sessionKey{}, p)
		// grpc-gateway has special handling for Authorization even when a custom
		// header matcher rejects it. Remove all caller RPC credentials first; only
		// the session-derived metadata callback may populate the upstream token.
		r = r.Clone(ctx)
		for name := range r.Header {
			if strings.EqualFold(name, "Authorization") || strings.HasPrefix(strings.ToLower(name), "grpc-metadata-") {
				r.Header.Del(name)
			}
		}
		if r.URL.Path != "/api/v1/entities/stream" {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		s.mux.ServeHTTP(w, r.WithContext(ctx))
	})
}
