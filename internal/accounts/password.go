// Package accounts 提供个人账号、密码与可撤销的不透明会话。
package accounts

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"example.com/meshops-course/internal/platform"
	"fmt"
	"golang.org/x/crypto/argon2"
	"strings"
	"unicode/utf8"
)

var (
	ErrInvalid     = errors.New("invalid account input")
	ErrCredentials = errors.New("invalid username or password")
	ErrConflict    = errors.New("username already exists")
	ErrForbidden   = errors.New("account operation forbidden")
	ErrNotFound    = errors.New("account not found")
	ErrBusy        = errors.New("password service busy; retry later")
)

type Hasher struct{ slots chan struct{} }

func NewHasher() *Hasher { return &Hasher{slots: make(chan struct{}, 2)} }
func validatePassword(p string) error {
	if !utf8.ValidString(p) || len(p) > 512 || utf8.RuneCountInString(p) < 12 || utf8.RuneCountInString(p) > 128 {
		return ErrInvalid
	}
	return nil
}
func normalizeUsername(v string) (string, error) {
	v = strings.ToLower(v)
	if len(v) < 3 || !platform.ValidID(v, 32) {
		return "", ErrInvalid
	}
	return v, nil
}
func (h *Hasher) acquire(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	select {
	case h.slots <- struct{}{}:
		return nil
	default:
		return ErrBusy
	}
}
func (h *Hasher) Hash(ctx context.Context, p string) (string, error) {
	if e := validatePassword(p); e != nil {
		return "", e
	}
	if e := h.acquire(ctx); e != nil {
		return "", e
	}
	defer func() { <-h.slots }()
	salt := make([]byte, 16)
	if _, e := rand.Read(salt); e != nil {
		return "", e
	}
	key := argon2.IDKey([]byte(p), salt, 3, 65536, 1, 32)
	return fmt.Sprintf("$argon2id$v=19$m=65536,t=3,p=1$%s$%s", base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}
func (h *Hasher) Verify(ctx context.Context, encoded, p string) error {
	if validatePassword(p) != nil || len(encoded) > 256 {
		return ErrCredentials
	}
	parts := strings.Split(encoded, "$")
	// 只接受当前记录的成本，先校验再分配内存，避免恶意参数耗尽资源。
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" || parts[3] != "m=65536,t=3,p=1" {
		return ErrCredentials
	}
	salt, e := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if e != nil || len(salt) != 16 {
		return ErrCredentials
	}
	want, e := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if e != nil || len(want) != 32 {
		return ErrCredentials
	}
	if e = h.acquire(ctx); e != nil {
		return e
	}
	defer func() { <-h.slots }()
	actual := argon2.IDKey([]byte(p), salt, 3, 65536, 1, 32)
	if subtle.ConstantTimeCompare(actual, want) != 1 {
		return ErrCredentials
	}
	return nil
}
