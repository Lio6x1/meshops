package accounts

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"example.com/meshops-course/internal/platform"
	"github.com/go-sql-driver/mysql"
	"strings"
	"time"
	"unicode/utf8"
)

type Account struct {
	ID                 string    `json:"id"`
	TenantID           string    `json:"-"`
	Username           string    `json:"username"`
	DisplayName        string    `json:"displayName"`
	Role               string    `json:"role"`
	Enabled            bool      `json:"enabled"`
	MustChangePassword bool      `json:"mustChangePassword"`
	CreatedAt          time.Time `json:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt"`
	passwordHash       string
	sessionHash        string
	version            int64
}

func (a Account) Principal() platform.Principal {
	return platform.Principal{ID: a.ID, TenantID: a.TenantID, Role: a.Role, AuthVersion: a.version, SessionHash: a.sessionHash}
}

type Store struct {
	db     *sql.DB
	hasher *Hasher
}

func NewStore(db *sql.DB) *Store { return &Store{db: db, hasher: NewHasher()} }

const columns = "id,tenant_id,username,display_name,role,enabled,must_change_password,created_at,updated_at,password_hash,auth_version"

type scanner interface{ Scan(...any) error }

func scan(row scanner) (a Account, e error) {
	e = row.Scan(&a.ID, &a.TenantID, &a.Username, &a.DisplayName, &a.Role, &a.Enabled, &a.MustChangePassword, &a.CreatedAt, &a.UpdatedAt, &a.passwordHash, &a.version)
	if errors.Is(e, sql.ErrNoRows) {
		e = ErrNotFound
	}
	return
}
func validAdmin(p platform.Principal) bool {
	return p.Role == "admin" && p.ID != "" && platform.ValidID(p.TenantID, 64)
}
func validateDisplay(v string) error {
	if !utf8.ValidString(v) || strings.TrimSpace(v) == "" || len(v) > 512 || utf8.RuneCountInString(v) > 128 {
		return ErrInvalid
	}
	return nil
}
func conflict(e error) error {
	var m *mysql.MySQLError
	if errors.As(e, &m) && m.Number == 1062 {
		return ErrConflict
	}
	return e
}
func audit(ctx context.Context, tx *sql.Tx, tenant, actor, target, action string) error {
	_, e := tx.ExecContext(ctx, "INSERT INTO account_audit(tenant_id,actor_id,target_id,action,created_at) VALUES(?,?,?,?,UTC_TIMESTAMP(6))", tenant, actor, target, action)
	return e
}
func (s *Store) Admin(ctx context.Context, tenant string) (Account, error) {
	if !platform.ValidID(tenant, 64) {
		return Account{}, ErrInvalid
	}
	return scan(s.db.QueryRowContext(ctx, "SELECT "+columns+" FROM accounts WHERE tenant_id=? AND role='admin'", tenant))
}
func (s *Store) Bootstrap(ctx context.Context, tenant, username, display, password string) (Account, bool, error) {
	if !platform.ValidID(tenant, 64) {
		return Account{}, false, ErrInvalid
	}
	u, e := normalizeUsername(username)
	if e != nil {
		return Account{}, false, e
	}
	if display == "" {
		display = u
	}
	if e = validateDisplay(display); e != nil {
		return Account{}, false, e
	}
	if e = validatePassword(password); e != nil {
		return Account{}, false, e
	}
	if a, e := s.Admin(ctx, tenant); e == nil {
		return a, false, nil
	} else if !errors.Is(e, ErrNotFound) {
		return Account{}, false, e
	}
	a, e := s.insert(ctx, tenant, u, display, password, "admin", platform.Principal{ID: "local-admin"}, false, true)
	if errors.Is(e, ErrConflict) {
		if old, x := s.Admin(ctx, tenant); x == nil {
			return old, false, nil
		}
	}
	return a, e == nil, e
}
func (s *Store) Create(ctx context.Context, p platform.Principal, username, display, password string) (Account, error) {
	if !validAdmin(p) {
		return Account{}, ErrForbidden
	}
	u, e := normalizeUsername(username)
	if e != nil {
		return Account{}, e
	}
	if e = validateDisplay(display); e != nil {
		return Account{}, e
	}
	return s.insert(ctx, p.TenantID, u, display, password, "operator", p, true, false)
}
func (s *Store) insert(ctx context.Context, tenant, username, display, password, role string, actor platform.Principal, must, local bool) (Account, error) {
	hash, e := s.hasher.Hash(ctx, password)
	if e != nil {
		return Account{}, e
	}
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return Account{}, e
	}
	defer tx.Rollback()
	if !local {
		if e = checkCaller(ctx, tx, actor, false); e != nil {
			return Account{}, e
		}
	}
	a := Account{ID: platform.NewID(), TenantID: tenant, Username: username, DisplayName: display, Role: role, Enabled: true, MustChangePassword: must, CreatedAt: time.Now().UTC(), version: 1}
	a.UpdatedAt = a.CreatedAt
	_, e = tx.ExecContext(ctx, "INSERT INTO accounts(id,tenant_id,username,display_name,role,enabled,must_change_password,password_hash,auth_version,created_at,updated_at) VALUES(?,?,?,?,?,TRUE,?,?,1,?,?)", a.ID, tenant, username, display, role, must, hash, a.CreatedAt, a.UpdatedAt)
	if e != nil {
		return Account{}, conflict(e)
	}
	if e = audit(ctx, tx, tenant, actor.ID, a.ID, "create"); e != nil {
		return Account{}, e
	}
	return a, tx.Commit()
}
func (s *Store) List(ctx context.Context, p platform.Principal, limit int, after string) ([]Account, string, error) {
	if !validAdmin(p) {
		return nil, "", ErrForbidden
	}
	if limit < 1 || limit > 100 || (after != "" && !platform.ValidID(after, 36)) {
		return nil, "", ErrInvalid
	}
	rows, e := s.db.QueryContext(ctx, "SELECT "+columns+" FROM accounts WHERE tenant_id=? AND id>? ORDER BY id LIMIT ?", p.TenantID, after, limit+1)
	if e != nil {
		return nil, "", e
	}
	defer rows.Close()
	out := []Account{}
	for rows.Next() {
		a, e := scan(rows)
		if e != nil {
			return nil, "", e
		}
		out = append(out, a)
	}
	if e = rows.Err(); e != nil {
		return nil, "", e
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		next = out[len(out)-1].ID
	}
	return out, next, nil
}
func (s *Store) Login(ctx context.Context, tenant, username, password string, ttl time.Duration) (Account, string, error) {
	if ttl < time.Minute || ttl > 24*time.Hour {
		return Account{}, "", ErrInvalid
	}
	u, e := normalizeUsername(username)
	if e != nil || validatePassword(password) != nil {
		return Account{}, "", ErrCredentials
	}
	a, e := scan(s.db.QueryRowContext(ctx, "SELECT "+columns+" FROM accounts WHERE tenant_id=? AND username=?", tenant, u))
	if e != nil && !errors.Is(e, ErrNotFound) {
		return Account{}, "", e
	}
	// 未知用户名也执行相同成本的哈希，统一错误与主要耗时。
	if errors.Is(e, ErrNotFound) {
		if _, x := s.hasher.Hash(ctx, password); x != nil {
			return Account{}, "", x
		}
		return Account{}, "", ErrCredentials
	}
	if e = s.hasher.Verify(ctx, a.passwordHash, password); e != nil {
		return Account{}, "", e
	}
	if !a.Enabled {
		return Account{}, "", ErrCredentials
	}
	raw := make([]byte, 32)
	if _, e = rand.Read(raw); e != nil {
		return Account{}, "", e
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return Account{}, "", e
	}
	defer tx.Rollback()
	current, e := scan(tx.QueryRowContext(ctx, "SELECT "+columns+" FROM accounts WHERE id=? FOR UPDATE", a.ID))
	if e != nil {
		return Account{}, "", e
	}
	if !current.Enabled || current.version != a.version {
		return Account{}, "", ErrCredentials
	}
	// 同用户及过期记录有界清理；数据库只保存内部令牌的 SHA-256。
	_, e = tx.ExecContext(ctx, "DELETE FROM account_sessions WHERE expires_at<=UTC_TIMESTAMP(6) LIMIT 256")
	if e != nil {
		return Account{}, "", e
	}
	var count int
	if e = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM account_sessions WHERE account_id=?", a.ID).Scan(&count); e != nil {
		return Account{}, "", e
	}
	if count >= 32 {
		return Account{}, "", ErrBusy
	}
	_, e = tx.ExecContext(ctx, "INSERT INTO account_sessions(token_hash,account_id,auth_version,expires_at) VALUES(?,?,?,?)", platform.Hash([]byte(token)), a.ID, a.version, time.Now().UTC().Add(ttl))
	if e != nil {
		return Account{}, "", e
	}
	if e = tx.Commit(); e != nil {
		return Account{}, "", e
	}
	current.sessionHash = platform.Hash([]byte(token))
	return current, token, nil
}
func (s *Store) CheckSession(ctx context.Context, token string) (Account, error) {
	if len(token) != 43 {
		return Account{}, ErrCredentials
	}
	// 子查询保持列名无歧义；禁用、认证版本和期限每次都查库。
	a, e := scan(s.db.QueryRowContext(ctx, "SELECT "+columns+" FROM accounts WHERE enabled=TRUE AND EXISTS (SELECT 1 FROM account_sessions s WHERE s.account_id=accounts.id AND s.auth_version=accounts.auth_version AND s.token_hash=? AND s.expires_at>UTC_TIMESTAMP(6))", platform.Hash([]byte(token))))
	if errors.Is(e, ErrNotFound) {
		e = ErrCredentials
	}
	a.sessionHash = platform.Hash([]byte(token))
	return a, e
}
func (s *Store) ResolveSession(ctx context.Context, token string) (platform.Principal, error) {
	a, e := s.CheckSession(ctx, token)
	if e != nil {
		return platform.Principal{}, e
	}
	if a.MustChangePassword {
		return platform.Principal{}, ErrForbidden
	}
	return a.Principal(), nil
}
func (s *Store) Logout(ctx context.Context, token string) error {
	_, e := s.db.ExecContext(ctx, "DELETE FROM account_sessions WHERE token_hash=?", platform.Hash([]byte(token)))
	return e
}
func (s *Store) SetEnabled(ctx context.Context, p platform.Principal, id string, enabled bool) (Account, error) {
	if !validAdmin(p) {
		return Account{}, ErrForbidden
	}
	return s.mutate(ctx, p, id, "operator", "set-enabled", false, func(a *Account) error { a.Enabled = enabled; return nil })
}
func (s *Store) ResetPassword(ctx context.Context, p platform.Principal, id, password string) error {
	if !validAdmin(p) {
		return ErrForbidden
	}
	hash, e := s.hasher.Hash(ctx, password)
	if e != nil {
		return e
	}
	_, e = s.mutate(ctx, p, id, "operator", "reset-password", false, func(a *Account) error { a.passwordHash = hash; a.MustChangePassword = true; return nil })
	return e
}
func (s *Store) ResetAdmin(ctx context.Context, tenant, username, password string) (Account, error) {
	u, e := normalizeUsername(username)
	if e != nil {
		return Account{}, e
	}
	a, e := s.Admin(ctx, tenant)
	if e != nil {
		return Account{}, e
	}
	if a.Username != u {
		return Account{}, ErrNotFound
	}
	hash, e := s.hasher.Hash(ctx, password)
	if e != nil {
		return Account{}, e
	}
	return s.mutate(ctx, platform.Principal{ID: "local-admin", TenantID: tenant, Role: "admin"}, a.ID, "admin", "reset-admin", true, func(a *Account) error {
		a.passwordHash = hash
		a.MustChangePassword = false
		a.Enabled = true
		return nil
	})
}
func (s *Store) ChangePassword(ctx context.Context, p platform.Principal, current, newPassword string) error {
	if p.ID == "" || p.TenantID == "" || (p.Role != "operator" && p.Role != "admin") {
		return ErrForbidden
	}
	a, e := scan(s.db.QueryRowContext(ctx, "SELECT "+columns+" FROM accounts WHERE tenant_id=? AND id=?", p.TenantID, p.ID))
	if e != nil {
		return e
	}
	if !a.Enabled {
		return ErrCredentials
	}
	if e = s.hasher.Verify(ctx, a.passwordHash, current); e != nil {
		return e
	}
	if current == newPassword {
		return ErrInvalid
	}
	hash, e := s.hasher.Hash(ctx, newPassword)
	if e != nil {
		return e
	}
	_, e = s.mutate(ctx, p, p.ID, "", "change-password", false, func(v *Account) error {
		if v.version != a.version || !v.Enabled {
			return ErrCredentials
		}
		v.passwordHash = hash
		v.MustChangePassword = false
		return nil
	})
	return e
}
func (s *Store) mutate(ctx context.Context, p platform.Principal, id, role, action string, local bool, change func(*Account) error) (Account, error) {
	tx, e := s.db.BeginTx(ctx, nil)
	if e != nil {
		return Account{}, e
	}
	defer tx.Rollback()
	a, e := scan(tx.QueryRowContext(ctx, "SELECT "+columns+" FROM accounts WHERE tenant_id=? AND id=? FOR UPDATE", p.TenantID, id))
	if !local {
		if callerErr := checkCaller(ctx, tx, p, action == "change-password"); callerErr != nil {
			return Account{}, callerErr
		}
	}
	if e != nil {
		return Account{}, e
	}
	if role != "" && a.Role != role {
		return Account{}, ErrForbidden
	}
	if e = change(&a); e != nil {
		return Account{}, e
	}
	a.UpdatedAt = time.Now().UTC()
	a.version++
	_, e = tx.ExecContext(ctx, "UPDATE accounts SET enabled=?,must_change_password=?,password_hash=?,auth_version=auth_version+1,updated_at=? WHERE tenant_id=? AND id=?", a.Enabled, a.MustChangePassword, a.passwordHash, a.UpdatedAt, p.TenantID, id)
	if e != nil {
		return Account{}, e
	}
	if _, e = tx.ExecContext(ctx, "DELETE FROM account_sessions WHERE account_id=?", id); e != nil {
		return Account{}, e
	}
	if e = audit(ctx, tx, p.TenantID, p.ID, id, action); e != nil {
		return Account{}, e
	}
	return a, tx.Commit()
}

// 与目标修改同一事务锁定调用者，防止密码重置后复用已解析的旧 Principal。
func checkCaller(ctx context.Context, tx *sql.Tx, p platform.Principal, allowMustChange bool) error {
	a, e := scan(tx.QueryRowContext(ctx, "SELECT "+columns+" FROM accounts WHERE tenant_id=? AND id=? FOR UPDATE", p.TenantID, p.ID))
	if errors.Is(e, ErrNotFound) {
		return ErrCredentials
	}
	if e != nil {
		return e
	}
	if !a.Enabled || p.AuthVersion <= 0 || a.version != p.AuthVersion || a.Role != p.Role {
		return ErrCredentials
	}
	if a.MustChangePassword && !allowMustChange {
		return ErrForbidden
	}
	if len(p.SessionHash) != 64 {
		return ErrCredentials
	}
	var active int
	e = tx.QueryRowContext(ctx, "SELECT 1 FROM account_sessions WHERE token_hash=? AND account_id=? AND auth_version=? AND expires_at>UTC_TIMESTAMP(6) FOR UPDATE", p.SessionHash, p.ID, p.AuthVersion).Scan(&active)
	if errors.Is(e, sql.ErrNoRows) {
		return ErrCredentials
	}
	if e != nil {
		return e
	}
	return nil
}
