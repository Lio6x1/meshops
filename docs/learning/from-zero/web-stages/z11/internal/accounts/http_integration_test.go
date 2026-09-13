//go:build integration

package accounts_test

import (
	"bufio"
	"context"
	"encoding/json"
	entityv1 "example.com/meshops-course/gen/entity/v1"
	"example.com/meshops-course/internal/accounts"
	"example.com/meshops-course/internal/platform"
	"example.com/meshops-course/internal/web"
	"google.golang.org/grpc"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type accountEntity struct {
	entityv1.UnimplementedEntityServiceServer
	stopped    chan struct{}
	identities chan platform.Principal
}

func (p *accountEntity) Subscribe(_ *entityv1.SubscribeRequest, stream entityv1.EntityService_SubscribeServer) error {
	p.identities <- platform.Identity(stream.Context())
	if e := stream.Send(&entityv1.EntityUpdate{Kind: entityv1.EntityUpdateKind_ENTITY_UPDATE_KIND_SNAPSHOT_END}); e != nil {
		return e
	}
	<-stream.Context().Done()
	p.stopped <- struct{}{}
	return stream.Context().Err()
}
func accountRequest(s *web.Server, method, path string, body any, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(body)
	r := httptest.NewRequest(method, "http://localhost:18090"+path, strings.NewReader(string(raw)))
	r.Header.Set("Origin", "http://localhost:18090")
	r.Header.Set("X-CSRF-Token", csrf)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}
func loginHTTP(t *testing.T, s *web.Server, username, password string) (*http.Cookie, string) {
	t.Helper()
	w := accountRequest(s, "POST", "/api/session", map[string]string{"username": username, "password": password}, nil, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var v struct {
		CSRF string `json:"csrfToken"`
	}
	if e := json.Unmarshal(w.Body.Bytes(), &v); e != nil {
		t.Fatal(e)
	}
	return w.Result().Cookies()[0], v.CSRF
}

func TestAccountHTTPSSERevocation(t *testing.T) {
	db := accountDB(t)
	store := accounts.NewStore(db)
	ctx := context.Background()
	admin, _, e := store.Bootstrap(ctx, "http_tenant", "admin", "Administrator", "admin-password-123")
	if e != nil {
		t.Fatal(e)
	}
	admin, _, e = store.Login(ctx, "http_tenant", "admin", "admin-password-123", time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	reg := &platform.Registry{ResolveSession: store.ResolveSession, Bindings: map[string]platform.Binding{platform.Key("http_tenant", "drone-001"): {TenantID: "http_tenant", EntityID: "drone-001"}}}
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	rpc := grpc.NewServer(grpc.UnaryInterceptor(reg.Unary()), grpc.StreamInterceptor(reg.Stream()))
	probe := &accountEntity{stopped: make(chan struct{}, 8), identities: make(chan platform.Principal, 8)}
	entityv1.RegisterEntityServiceServer(rpc, probe)
	go func() { _ = rpc.Serve(listener) }()
	defer rpc.Stop()
	conn, e := platform.Dial(listener.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	s, e := web.New(web.Config{Accounts: store, Registry: reg, TenantID: "http_tenant", Entity: entityv1.NewEntityServiceClient(conn), AllowedOrigins: []string{"http://localhost:18090"}})
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(s.Handler())
	defer server.Close()
	ac, acsrf := loginHTTP(t, s, "admin", "admin-password-123")
	for _, operation := range []string{"disable", "reset", "password", "logout", "external-reset"} {
		t.Run(operation, func(t *testing.T) {
			body := map[string]string{"username": "user_" + strings.ReplaceAll(operation, "-", "_"), "displayName": "Operator", "password": "temporary-password-123"}
			w := accountRequest(s, "POST", "/api/accounts", body, ac, acsrf)
			if w.Code != 201 {
				t.Fatal(w.Code, w.Body.String())
			}
			var a accounts.Account
			if e := json.Unmarshal(w.Body.Bytes(), &a); e != nil {
				t.Fatal(e)
			}
			oc, ocsrf := loginHTTP(t, s, body["username"], body["password"])
			if w = accountRequest(s, "GET", "/api/accounts", nil, oc, ""); w.Code != 403 {
				t.Fatal(w.Code)
			}
			if w = accountRequest(s, "POST", "/api/account/password", map[string]string{"currentPassword": body["password"], "newPassword": "permanent-password-123"}, oc, ocsrf); w.Code != 204 {
				t.Fatal(w.Code, w.Body.String())
			}
			if w = accountRequest(s, "GET", "/api/session", nil, oc, ""); w.Code != 401 {
				t.Fatal(w.Code)
			}
			oc, ocsrf = loginHTTP(t, s, body["username"], "permanent-password-123")
			request, e := http.NewRequest("GET", server.URL+"/api/v1/entities/stream?entity_ids=drone-001", nil)
			if e != nil {
				t.Fatal(e)
			}
			request.Host = "localhost:18090"
			request.AddCookie(oc)
			request.Header.Set("Authorization", "Bearer forged-admin")
			client := &http.Client{Timeout: 10 * time.Second}
			response, e := client.Do(request)
			if e != nil {
				t.Fatal(e)
			}
			defer response.Body.Close()
			if response.StatusCode != 200 {
				t.Fatal(response.StatusCode)
			}
			reader := bufio.NewReader(response.Body)
			if line, e := reader.ReadString('\n'); e != nil || line != "event: update\n" {
				t.Fatal(line, e)
			}
			select {
			case identity := <-probe.identities:
				if identity.ID != a.ID || identity.Role != "operator" || identity.TenantID != "http_tenant" {
					t.Fatal(identity)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("missing RPC identity")
			}
			switch operation {
			case "disable":
				w = accountRequest(s, "PATCH", "/api/accounts/"+a.ID, map[string]bool{"enabled": false}, ac, acsrf)
				if w.Code != 200 {
					t.Fatal(w.Code, w.Body.String())
				}
			case "reset":
				w = accountRequest(s, "POST", "/api/accounts/"+a.ID+"/reset-password", map[string]string{"password": "reset-password-123"}, ac, acsrf)
				if w.Code != 204 {
					t.Fatal(w.Code, w.Body.String())
				}
			case "password":
				w = accountRequest(s, "POST", "/api/account/password", map[string]string{"currentPassword": "permanent-password-123", "newPassword": "replacement-password-123"}, oc, ocsrf)
				if w.Code != 204 {
					t.Fatal(w.Code, w.Body.String())
				}
			case "logout":
				w = accountRequest(s, "DELETE", "/api/session", nil, oc, ocsrf)
				if w.Code != 204 {
					t.Fatal(w.Code, w.Body.String())
				}
			case "external-reset":
				if e = store.ResetPassword(ctx, admin.Principal(), a.ID, "reset-password-123"); e != nil {
					t.Fatal(e)
				}
			}
			select {
			case <-probe.stopped:
			case <-time.After(4 * time.Second):
				t.Fatal("revocation left upstream SSE active")
			}
			if w = accountRequest(s, "GET", "/api/session", nil, oc, ""); w.Code != 401 {
				t.Fatal("revoked HTTP session", w.Code)
			}
		})
	}
}
