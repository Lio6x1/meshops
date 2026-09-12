package search

import (
	"context"
	"strings"
	"testing"

	searchv1 "example.com/meshops-course/gen/search/v1"
	"example.com/meshops-course/internal/platform"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type queryStub struct{ tenant string }

func (q *queryStub) Search(_ context.Context, tenant string, _ Filter, _ string, _ []byte) (Page, error) {
	q.tenant = tenant
	return Page{}, nil
}

func TestSearchServiceUsesTrustedTenantAndReadiness(t *testing.T) {
	q := &queryStub{}
	s := &Service{Index: q, CursorKey: []byte(strings.Repeat("k", 32)), Ready: func() bool { return true }}
	for _, p := range []platform.Principal{{}, {TenantID: "a", Role: "source"}, {TenantID: "a", Role: "executor"}} {
		if _, err := s.SearchTasks(platform.WithPrincipal(context.Background(), p), &searchv1.SearchTasksRequest{}); status.Code(err) != codes.PermissionDenied {
			t.Fatalf("role admitted: %+v %v", p, err)
		}
	}
	ctx := platform.WithPrincipal(context.Background(), platform.Principal{TenantID: "trusted-a", Role: "operator"})
	if _, err := s.SearchTasks(ctx, &searchv1.SearchTasksRequest{}); err != nil || q.tenant != "trusted-a" {
		t.Fatalf("tenant %s err %v", q.tenant, err)
	}
	s.Ready = func() bool { return false }
	if _, err := s.SearchTasks(ctx, &searchv1.SearchTasksRequest{}); status.Code(err) != codes.Unavailable {
		t.Fatalf("bootstrap not ready: %v", err)
	}
}
