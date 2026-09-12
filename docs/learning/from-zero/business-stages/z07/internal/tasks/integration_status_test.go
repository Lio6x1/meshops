//go:build integration

package tasks

import (
	"context"
	"errors"
	"testing"

	dispatcherv1 "example.com/meshops-course/gen/dispatcher/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestDispatcherStatusRequiresLagDependency(t *testing.T) {
	f := fixtureFor(t)
	response, err := f.dispatchClient.GetStatus(f.ctx("tenant", "admin"), &dispatcherv1.GetStatusRequest{})
	if status.Code(err) != codes.Unavailable || response != nil {
		t.Fatalf("unconfigured lag returned response=%v error=%v", response, err)
	}
}

func TestDispatcherStatusReportsLagAndTenantCounts(t *testing.T) {
	f := fixtureFor(t)
	task := create(t, f, "status-lag")
	persistCreated(t, f, task)
	f.dispatcher.cfg.ConsumerGroupPrefix = "course_test_"
	f.dispatcher.cfg.TopicPrefix = "course_test_topics_"
	type contextKey struct{}
	ctx := context.WithValue(f.local("tenant", "admin"), contextKey{}, "propagated")
	called := false
	f.dispatcher.SetLagReader(func(ctx context.Context, group, topic string) (int64, error) {
		called = true
		if group != "course_test_dispatcher" || topic != "course_test_topics_task-events.v1" {
			t.Errorf("lag queried wrong group/topic: %q %q", group, topic)
		}
		if ctx.Value(contextKey{}) != "propagated" {
			t.Error("caller context was lost")
		}
		return 17, nil
	})
	response, err := f.dispatcher.GetStatus(ctx, &dispatcherv1.GetStatusRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !called || response.ConsumerLag != 17 || response.ActiveTasks != 1 || response.DlqCount != 0 || response.AsOf == nil {
		t.Fatalf("incorrect status: called=%v response=%v", called, response)
	}
	f.dispatcher.SetLagReader(func(context.Context, string, string) (int64, error) { return 0, nil })
	response, err = f.dispatchClient.GetStatus(f.ctx("other", "admin"), &dispatcherv1.GetStatusRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if response.ConsumerLag != 0 || response.ActiveTasks != 0 || response.DlqCount != 0 {
		t.Fatalf("tenant isolation/zero lag: %v", response)
	}
}

func TestDispatcherStatusPropagatesLagFailure(t *testing.T) {
	f := fixtureFor(t)
	f.dispatcher.SetLagReader(func(context.Context, string, string) (int64, error) { return 99, errors.New("broker unavailable") })
	response, err := f.dispatchClient.GetStatus(f.ctx("tenant", "admin"), &dispatcherv1.GetStatusRequest{})
	if status.Code(err) != codes.Unavailable || response != nil {
		t.Fatalf("failed lag returned response=%v error=%v", response, err)
	}
	// 必须先鉴权，再访问依赖。
	f.dispatcher.SetLagReader(func(context.Context, string, string) (int64, error) {
		t.Error("unauthorized caller queried lag")
		return 0, nil
	})
	_, err = f.dispatchClient.GetStatus(f.ctx("tenant", "operator"), &dispatcherv1.GetStatusRequest{})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("status authorization: %v", err)
	}
}
