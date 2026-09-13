package accounts

import (
	"context"
	"errors"
	"example.com/meshops-course/internal/platform"
	"testing"
)

// 输入与权限必须在接触数据库前拒绝，避免越权调用产生副作用。
func TestStoreRejectsUntrustedManagement(t *testing.T) {
	s := NewStore(nil)
	ctx := context.Background()
	if _, e := s.Create(ctx, platform.Principal{ID: "u", TenantID: "t", Role: "operator"}, "valid-user", "Name", "valid-password-123"); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	if _, e := s.Create(ctx, platform.Principal{ID: "u", TenantID: "t", Role: "admin"}, "bad name", "Name", "valid-password-123"); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
	if _, _, e := s.List(ctx, platform.Principal{ID: "u", TenantID: "t", Role: "operator"}, 20, ""); !errors.Is(e, ErrForbidden) {
		t.Fatal(e)
	}
	if _, _, e := s.List(ctx, platform.Principal{ID: "u", TenantID: "t", Role: "admin"}, 1001, ""); !errors.Is(e, ErrInvalid) {
		t.Fatal(e)
	}
}
