//go:build integration

package accounts_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"example.com/meshops-course/internal/accounts"
	"example.com/meshops-course/internal/platform"
	"example.com/meshops-course/internal/tasks"
	"github.com/go-sql-driver/mysql"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func accountDB(t *testing.T) *sql.DB {
	t.Helper()
	raw := os.Getenv("MESHOPS_TEST_MYSQL_DSN")
	if raw == "" {
		t.Fatal("MESHOPS_TEST_MYSQL_DSN required for isolated MySQL8 integration")
	}
	cfg, e := mysql.ParseDSN(raw)
	if e != nil {
		t.Fatal("invalid MySQL test DSN")
	}
	cfg.DBName = ""
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	if cfg.Params == nil {
		cfg.Params = map[string]string{}
	}
	cfg.Params["time_zone"] = "'+00:00'"
	admin, e := sql.Open("mysql", cfg.FormatDSN())
	if e != nil {
		t.Fatal(e)
	}
	name := "account_test_" + strings.ReplaceAll(platform.NewID(), "-", "")
	if _, e = admin.Exec("CREATE DATABASE `" + name + "` CHARACTER SET utf8mb4"); e != nil {
		admin.Close()
		t.Fatal(e)
	}
	cfg.DBName = name
	db, e := sql.Open("mysql", cfg.FormatDSN())
	if e != nil {
		t.Fatal(e)
	}
	db.SetMaxOpenConns(8)
	t.Cleanup(func() {
		db.Close()
		if !strings.HasPrefix(name, "account_test_") || len(name) != 45 {
			t.Error("unsafe database cleanup target")
			return
		}
		if _, e := admin.Exec("DROP DATABASE `" + name + "`"); e != nil {
			t.Error(e)
		}
		admin.Close()
	})
	if e = tasks.Migrate(context.Background(), db, "../../migrations"); e != nil {
		t.Fatal(e)
	}
	return db
}

func TestAccountsMySQLLifecycleAndPersonalRPC(t *testing.T) {
	db := accountDB(t)
	s := accounts.NewStore(db)
	ctx := context.Background()
	const first = "initial-admin-password-123"
	const temporary = "temporary-password-123"
	const permanent = "permanent-password-123"
	admin, created, e := s.Bootstrap(ctx, "tenant_a", "Admin", "Administrator", first)
	if e != nil || !created {
		t.Fatal(created, e)
	}
	if admin.MustChangePassword || admin.Username != "admin" {
		t.Fatal(admin)
	}
	if a, c, e := s.Bootstrap(ctx, "tenant_a", "different", "Different", permanent); e != nil || c || a.ID != admin.ID {
		t.Fatal("bootstrap overwrote", a, c, e)
	}
	if e = tasks.Migrate(ctx, db, "../../migrations"); e != nil {
		t.Fatal("migration reentry", e)
	}
	a, adminToken, e := s.Login(ctx, "tenant_a", "ADMIN", first, time.Hour)
	if e != nil || a.ID != admin.ID {
		t.Fatal(e)
	}
	admin = a
	op, e := s.Create(ctx, admin.Principal(), "alice", "Alice", temporary)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Create(ctx, admin.Principal(), "ALICE", "Alice", temporary); !errors.Is(e, accounts.ErrConflict) {
		t.Fatal("duplicate", e)
	}
	op, token, e := s.Login(ctx, "tenant_a", "alice", temporary, time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.ResolveSession(ctx, token); !errors.Is(e, accounts.ErrForbidden) {
		t.Fatal("must-change RPC allowed", e)
	}
	var stored string
	if e = db.QueryRow("SELECT token_hash FROM account_sessions WHERE account_id=?", op.ID).Scan(&stored); e != nil || stored == token || stored != platform.Hash([]byte(token)) {
		t.Fatal("session hash", e)
	}
	if _, _, e = s.Login(ctx, "tenant_a", "missing", temporary, time.Hour); !errors.Is(e, accounts.ErrCredentials) {
		t.Fatal(e)
	}
	if _, _, e = s.Login(ctx, "tenant_a", "alice", permanent, time.Hour); !errors.Is(e, accounts.ErrCredentials) {
		t.Fatal(e)
	}
	if e = s.ChangePassword(ctx, op.Principal(), temporary, permanent); e != nil {
		t.Fatal(e)
	}
	if _, e = s.CheckSession(ctx, token); !errors.Is(e, accounts.ErrCredentials) {
		t.Fatal("old session survived change", e)
	}
	op, token, e = s.Login(ctx, "tenant_a", "alice", permanent, time.Hour)
	if e != nil || op.MustChangePassword {
		t.Fatal(e)
	}
	registry := &platform.Registry{ResolveSession: s.ResolveSession}
	rpcCtx := metadata.NewIncomingContext(ctx, metadata.Pairs("authorization", "Bearer "+token, "role", "admin", "actor-id", "forged"))
	verifyRPC := func(want codes.Code) {
		t.Helper()
		got, e := registry.Unary()(rpcCtx, nil, &grpc.UnaryServerInfo{FullMethod: "/meshops.task.v1.TaskService/CreateTask"}, func(c context.Context, _ any) (any, error) { return platform.Identity(c), nil })
		if status.Code(e) != want {
			t.Fatal(e)
		}
		if e == nil {
			p := got.(platform.Principal)
			if p.ID != op.ID || p.Role != "operator" || p.TenantID != "tenant_a" {
				t.Fatal("forged identity", p)
			}
		}
	}
	verifyRPC(codes.OK)
	otherAdmin, _, e := s.Bootstrap(ctx, "tenant_b", "admin", "Other admin", first)
	if e != nil {
		t.Fatal(e)
	}
	otherAdmin, _, e = s.Login(ctx, "tenant_b", "admin", first, time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	other, e := s.Create(ctx, otherAdmin.Principal(), "alice", "Other", temporary)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.SetEnabled(ctx, admin.Principal(), other.ID, false); !errors.Is(e, accounts.ErrNotFound) {
		t.Fatal("cross-tenant disable", e)
	}
	if _, e = s.SetEnabled(ctx, admin.Principal(), admin.ID, false); !errors.Is(e, accounts.ErrForbidden) {
		t.Fatal("admin disabled", e)
	}
	if e = s.ResetPassword(ctx, admin.Principal(), admin.ID, permanent); !errors.Is(e, accounts.ErrForbidden) {
		t.Fatal("admin reset through operator API", e)
	}
	page, next, e := s.List(ctx, admin.Principal(), 1, "")
	if e != nil || len(page) != 1 || next == "" {
		t.Fatal(page, next, e)
	}
	tail, last, e := s.List(ctx, admin.Principal(), 1, next)
	if e != nil || len(tail) != 1 || tail[0].ID == page[0].ID || last != "" {
		t.Fatal(tail, last, e)
	}
	raw, _ := json.Marshal(op)
	for _, secret := range []string{"password_hash", "passwordHash", permanent, token, "tenantId"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("secret response", string(raw))
		}
	}
	if _, e = s.SetEnabled(ctx, admin.Principal(), op.ID, false); e != nil {
		t.Fatal(e)
	}
	verifyRPC(codes.Unauthenticated)
	if _, _, e = s.Login(ctx, "tenant_a", "alice", permanent, time.Hour); !errors.Is(e, accounts.ErrCredentials) {
		t.Fatal("disabled login", e)
	}
	if _, e = s.SetEnabled(ctx, admin.Principal(), op.ID, true); e != nil {
		t.Fatal(e)
	}
	if e = s.ResetPassword(ctx, admin.Principal(), op.ID, temporary); e != nil {
		t.Fatal(e)
	}
	op, token, e = s.Login(ctx, "tenant_a", "alice", temporary, time.Hour)
	if e != nil || !op.MustChangePassword {
		t.Fatal(e)
	}
	if e = s.Logout(ctx, token); e != nil {
		t.Fatal(e)
	}
	if _, e = s.CheckSession(ctx, token); !errors.Is(e, accounts.ErrCredentials) {
		t.Fatal("logout retained credential", e)
	}
	if _, e = s.ResetAdmin(ctx, "tenant_a", "admin", permanent); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Create(ctx, admin.Principal(), "stale_admin", "Stale", temporary); !errors.Is(e, accounts.ErrCredentials) {
		t.Fatal("revoked caller principal created operator", e)
	}
	if _, e = s.CheckSession(ctx, adminToken); !errors.Is(e, accounts.ErrCredentials) {
		t.Fatal("admin recovery retained credential", e)
	}
	var auditCount int
	if e = db.QueryRow("SELECT COUNT(*) FROM account_audit WHERE tenant_id='tenant_a' AND actor_id=? AND target_id=?", admin.ID, op.ID).Scan(&auditCount); e != nil || auditCount < 4 {
		t.Fatal("personal management audit", auditCount, e)
	}
}

func TestConcurrentBootstrapCreatesExactlyOneAdmin(t *testing.T) {
	db := accountDB(t)
	s := accounts.NewStore(db)
	ctx := context.Background()
	var wg sync.WaitGroup
	type result struct {
		a       accounts.Account
		created bool
		e       error
	}
	results := make(chan result, 2)
	for _, u := range []string{"admin_one", "admin_two"} {
		wg.Add(1)
		go func(u string) {
			defer wg.Done()
			a, c, e := s.Bootstrap(ctx, "concurrent", u, u, "concurrent-password-123")
			results <- result{a, c, e}
		}(u)
	}
	wg.Wait()
	close(results)
	created := 0
	id := ""
	for r := range results {
		if r.e != nil {
			t.Fatal(r.e)
		}
		if r.created {
			created++
		}
		if id != "" && id != r.a.ID {
			t.Fatal("different administrators")
		}
		id = r.a.ID
	}
	if created != 1 {
		t.Fatal("created", created)
	}
	var count int
	if e := db.QueryRow("SELECT COUNT(*) FROM accounts WHERE tenant_id='concurrent'").Scan(&count); e != nil || count != 1 {
		t.Fatal(count, e)
	}
}

func TestLoggedOutPrincipalCannotMutate(t *testing.T) {
	db := accountDB(t)
	s := accounts.NewStore(db)
	ctx := context.Background()
	if _, _, e := s.Bootstrap(ctx, "logout_tenant", "admin", "Admin", "initial-password-123"); e != nil {
		t.Fatal(e)
	}
	a, token, e := s.Login(ctx, "logout_tenant", "admin", "initial-password-123", time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Logout(ctx, token); e != nil {
		t.Fatal(e)
	}
	if _, e = s.Create(ctx, a.Principal(), "operator", "Operator", "temporary-password-123"); !errors.Is(e, accounts.ErrCredentials) {
		t.Fatal("logged-out principal mutated database", e)
	}
}
