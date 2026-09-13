package accounts

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestPasswordPolicyAndArgonBounds(t *testing.T) {
	h := NewHasher()
	for _, p := range []string{"short", strings.Repeat("a", 129), string([]byte{0xff})} {
		if _, err := h.Hash(context.Background(), p); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid password accepted", err)
		}
	}
	p := "a long 密码 with spaces "
	a, e := h.Hash(context.Background(), p)
	if e != nil {
		t.Fatal(e)
	}
	b, e := h.Hash(context.Background(), p)
	if e != nil || a == b {
		t.Fatal("independent salt missing", e)
	}
	if e = h.Verify(context.Background(), a, p); e != nil {
		t.Fatal(e)
	}
	if e = h.Verify(context.Background(), a, p+"x"); !errors.Is(e, ErrCredentials) {
		t.Fatal(e)
	}
	for _, v := range []string{strings.Replace(a, "m=65536", "m=4294967295", 1), strings.Replace(a, "t=3", "t=999", 1), strings.Replace(a, "p=1", "p=255", 1), a + "x"} {
		if e = h.Verify(context.Background(), v, p); !errors.Is(e, ErrCredentials) {
			t.Fatal("unbounded encoding accepted", e)
		}
	}
}
func TestUsernameCanonicalization(t *testing.T) {
	if v, e := normalizeUsername("Admin_01"); e != nil || v != "admin_01" {
		t.Fatal(v, e)
	}
	for _, v := range []string{"ab", " abc", "管理员", strings.Repeat("x", 33)} {
		if _, e := normalizeUsername(v); !errors.Is(e, ErrInvalid) {
			t.Fatal(v, e)
		}
	}
}

func BenchmarkArgon2idHash(b *testing.B) {
	h := NewHasher()
	b.ReportAllocs()
	for b.Loop() {
		if _, e := h.Hash(context.Background(), "benchmark-password-123"); e != nil {
			b.Fatal(e)
		}
	}
}

// 校验字符边界，允许演示使用八位或九位密码，保持 Unicode 和字节上限。
func TestPasswordLengthBoundary(t *testing.T) {
	for _, p := range []string{"demo1234", "demo12345", strings.Repeat("锁", 8)} {
		if err := validatePassword(p); err != nil {
			t.Errorf("valid length rejected: %v", err)
		}
	}
	for _, p := range []string{"demo123", strings.Repeat("锁", 7)} {
		if !errors.Is(validatePassword(p), ErrInvalid) {
			t.Error("short password accepted")
		}
	}
}
