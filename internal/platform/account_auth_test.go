package platform

import (
	"context"
	"errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"testing"
)

func TestPersonalResolverIdentityAndFailClosed(t *testing.T) {
	r := &Registry{ResolveSession: func(context.Context, string) (Principal, error) {
		return Principal{ID: "user-stable", TenantID: "tenant", Role: "operator"}, nil
	}}
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("authorization", "Bearer opaque", "role", "admin", "actorid", "forged"))
	got, e := r.authorize(ctx, "/meshops.task.v1.TaskService/CreateTask")
	if e != nil || Identity(got).ID != "user-stable" || Identity(got).Role != "operator" {
		t.Fatal(got, e)
	}
	r.ResolveSession = func(context.Context, string) (Principal, error) {
		return Principal{}, errors.New("database unavailable")
	}
	if _, e = r.authorize(ctx, "/meshops.task.v1.TaskService/CreateTask"); status.Code(e) != codes.Unauthenticated {
		t.Fatal(e)
	}
	r.ResolveSession = func(context.Context, string) (Principal, error) {
		return Principal{ID: "x", TenantID: "tenant", Role: "task_service"}, nil
	}
	if _, e = r.authorize(ctx, "/meshops.entity.v1.EntityService/GetSnapshot"); status.Code(e) != codes.Unauthenticated {
		t.Fatal("personal machine escalation", e)
	}
}
