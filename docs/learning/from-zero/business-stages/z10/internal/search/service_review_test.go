package search

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	searchv1 "example.com/meshops-course/gen/search/v1"
	"example.com/meshops-course/internal/platform"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type failingQuery struct{ err error }

func (q failingQuery) Search(context.Context, string, Filter, string, []byte) (Page, error) {
	return Page{}, q.err
}

// RPC 调用者必须能区分分页快照过期（重新搜索）与依赖不可用（稍后重试），
// 同时不能暴露后端细节。
func TestSearchServiceErrorContract(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		code codes.Code
	}{
		{"expired", fmt.Errorf("wrapped: %w", ErrSearchExpired), codes.FailedPrecondition},
		{"invalid", fmt.Errorf("wrapped: %w", ErrInvalidSearch), codes.InvalidArgument},
		{"dependency", errors.New("secret backend diagnostic"), codes.Unavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := &Service{Index: failingQuery{test.err}, CursorKey: []byte(strings.Repeat("k", 32)), Ready: func() bool { return true }}
			ctx := platform.WithPrincipal(context.Background(), platform.Principal{TenantID: "a", Role: "operator"})
			_, err := s.SearchTasks(ctx, &searchv1.SearchTasksRequest{})
			if status.Code(err) != test.code || strings.Contains(err.Error(), "secret") {
				t.Fatal(err)
			}
		})
	}
}
