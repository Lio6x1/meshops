package tasks

import (
	"context"
	commonv1 "example.com/meshops-course/gen/common/v1"
	executorv1 "example.com/meshops-course/gen/executor/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"testing"
)

func TestExecutorQueueBoundsIdentityAndCommandDedup(t *testing.T) {
	command := &executorv1.ListenTasksResponse{CommandId: "task-execute", DispatchId: "task-execute-2", Task: &commonv1.Task{TargetEntityId: "drone-001"}}
	c := &connection{queue: make(chan queuedCommand, 100), done: make(chan struct{}), entities: []string{"drone-001"}, pending: map[string]bool{"task-execute": true}}
	d := &Dispatcher{streams: map[string]*connection{"tenant:executor": c}}
	if e := d.send(context.Background(), "tenant", "executor", command); e == nil {
		t.Fatal("second attempt of queued command accepted")
	}
	if len(c.queue) != 0 {
		t.Fatal("duplicate occupied a queue slot")
	}
	delete(c.pending, "task-execute")
	c.bytes = 8 << 20
	if e := d.send(context.Background(), "tenant", "executor", command); status.Code(e) != codes.ResourceExhausted {
		t.Fatal("byte capacity ignored", e)
	}
	c.bytes = 0
	for i := 0; i < 100; i++ {
		c.queue <- queuedCommand{}
	}
	if e := d.send(context.Background(), "tenant", "executor", command); status.Code(e) != codes.ResourceExhausted {
		t.Fatal("item capacity ignored", e)
	}
	command.Task.TargetEntityId = "other-entity"
	if e := d.send(context.Background(), "tenant", "executor", command); status.Code(e) != codes.PermissionDenied {
		t.Fatal("entity binding ignored", e)
	}
	if e := d.send(context.Background(), "other", "executor", command); e == nil {
		t.Fatal("cross tenant stream selected")
	}
}
