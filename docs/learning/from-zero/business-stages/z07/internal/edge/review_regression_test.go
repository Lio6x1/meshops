package edge

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	commonv1 "example.com/meshops-course/gen/common/v1"
	executorv1 "example.com/meshops-course/gen/executor/v1"
	taskv1 "example.com/meshops-course/gen/task/v1"
	"example.com/meshops-course/internal/platform"
	bolt "go.etcd.io/bbolt"
	bolterrors "go.etcd.io/bbolt/errors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestExecutorIgnoresInvalidCommandsAndContinuesSameStream(t *testing.T) {
	inbox, err := OpenInbox(filepath.Join(t.TempDir(), "inbox.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer inbox.Close()
	good := command("good", false)
	good.Task.Status = commonv1.TaskStatus_TASK_STATUS_DISPATCH_PENDING
	good.Task.StatusVersion = 1
	s := &terminalRecoveryServer{executionServer: &executionServer{tasks: map[string]*commonv1.Task{"good": good.Task}, reports: map[string]*taskv1.ReportTaskStatusRequest{}, lost: true}, attempted: make(chan *taskv1.ReportTaskStatusRequest, 20), commands: make(chan *executorv1.ListenTasksResponse, 8)}
	foreign := command("foreign", false)
	foreign.Task.ExecutorId = "other"
	badID := command("bad-id", false)
	badID.CommandId = "not-deterministic"
	badPayload := command("bad-payload", false)
	badPayload.Task.Payload.PayloadJson = `{"duration_seconds":0}`
	if _, err = inbox.Accept(good); err != nil {
		t.Fatal(err)
	}
	conflict := proto.Clone(good).(*executorv1.ListenTasksResponse)
	conflict.Task.TargetEntityId = "different"
	for _, cmd := range []*executorv1.ListenTasksResponse{foreign, badID, badPayload, conflict, good} {
		s.commands <- cmd
	}
	conn := recoveryConnection(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	done := make(chan error, 1)
	stats := new(ExecutorStats)
	go func() {
		done <- RunExecutor(ctx, inbox, executorv1.NewExecutorServiceClient(conn), taskv1.NewTaskServiceClient(conn), "executor", "normal", 1, stats)
	}()
	defer func() { cancel(); <-done }()
	for {
		entry, err := inbox.Entry("good")
		if err != nil {
			t.Fatal(err)
		}
		if entry.Done {
			break
		}
		select {
		case err := <-done:
			done <- err
			t.Fatalf("invalid command stopped unrelated work: %v", err)
		default:
		}
		if err = pause(ctx, 10*time.Millisecond); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range []string{"foreign", "bad-id", "bad-payload"} {
		if _, err := inbox.Entry(key); err == nil {
			t.Fatalf("invalid command %s was persisted", key)
		}
	}
	if count, err := inbox.EffectCount("good"); err != nil || count != 1 {
		t.Fatal(count, err)
	}
	if stats.InvalidCommands.Load() != 4 || stats.DuplicateCommands.Load() != 1 {
		t.Fatal("invalid commands were not isolated and counted", stats.InvalidCommands.Load(), stats.DuplicateCommands.Load())
	}
}

func TestExecutorStorageFailureRemainsFatal(t *testing.T) {
	inbox, err := OpenInbox(filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err = inbox.Close(); err != nil {
		t.Fatal(err)
	}
	s := &terminalRecoveryServer{executionServer: &executionServer{}, commands: make(chan *executorv1.ListenTasksResponse, 1)}
	s.commands <- command("good", false)
	conn := recoveryConnection(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err = RunExecutor(ctx, inbox, executorv1.NewExecutorServiceClient(conn), taskv1.NewTaskServiceClient(conn), "executor", "normal", 1, new(ExecutorStats))
	if !errors.Is(err, bolterrors.ErrDatabaseNotOpen) {
		t.Fatalf("lost durability must stop runtime: %v", err)
	}
}

func TestContinuousUploadWaitsForGenerationAndReplaysAfterLoss(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(fmt.Sprint(lost), func(t *testing.T) {
			s := &uploadServer{lostFirst: lost}
			client := uploadClient(t, s)
			q, err := OpenQueue(filepath.Join(t.TempDir(), "queue.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer q.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			done := make(chan error, 1)
			go func() { done <- Upload(ctx, q, client, 100, 1000, false, new(UplinkStats)) }()
			defer func() { cancel(); <-done }()
			// 空队列时持续模式不能半关闭或返回；之后产生的数据仍属于本次运行。
			select {
			case err := <-done:
				done <- err
				t.Fatalf("empty live upload stopped: %v", err)
			case <-time.After(75 * time.Millisecond):
			}
			for version := int64(1); version <= 2; version++ {
				generate(t, q)
				for {
					snapshot, err := q.Stats()
					if err != nil {
						t.Fatal(err)
					}
					if snapshot.ConfirmedSequence == version && snapshot.Pending == 0 {
						break
					}
					select {
					case err := <-done:
						done <- err
						t.Fatalf("live upload stopped: %v", err)
					default:
					}
					if err = pause(ctx, 10*time.Millisecond); err != nil {
						t.Fatal(err)
					}
				}
				// 两次生成之间让上传器再次进入空队列等待分支。
				if err = pause(ctx, 50*time.Millisecond); err != nil {
					t.Fatal(err)
				}
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.closed {
				t.Fatal("continuous stream was half-closed after draining")
			}
			if lost {
				if len(s.requests) != 3 || !proto.Equal(s.requests[0], s.requests[1]) || s.requests[2].FirstSequence != 2 {
					t.Fatal("uncertain live batch changed on replay", s.requests)
				}
			} else if len(s.requests) != 2 || s.requests[1].FirstSequence != 2 {
				t.Fatal(s.requests)
			}
		})
	}
}

func TestGatewayJoinPreservesUploaderFailureAtGenerationEnd(t *testing.T) {
	// 模拟生成器达到 count 的同时上传器已经结束。关闭 runCtx 不能覆盖已发生的错误。
	for _, want := range []error{status.Error(codes.PermissionDenied, "credential revoked"), status.Error(codes.FailedPrecondition, "invalid ACK"), context.DeadlineExceeded} {
		ctx, cancel := context.WithCancel(context.Background())
		result := make(chan error, 1)
		result <- want
		cancel()
		if ctx.Err() == nil {
			t.Fatal("expected shutdown")
		}
		if got := joinUpload(result); got != want {
			t.Fatalf("uploader failure lost during shutdown: got %v want %v", got, want)
		}
	}
	for _, cancelled := range []error{context.Canceled, status.Error(codes.Canceled, "stream closed")} {
		result := make(chan error, 1)
		result <- cancelled
		if err := joinUpload(result); err != nil {
			t.Fatal("normal shutdown became a failure", err)
		}
	}
}

func TestQueueVersionBoundarySurvivesReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.db")
	q, err := OpenQueue(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = q.Enqueue(event(platform.MaxEntityVersion)); err != nil {
		t.Fatal(err)
	}
	if _, err = q.Enqueue(event(platform.MaxEntityVersion + 1)); err == nil {
		t.Fatal("out-of-range version admitted")
	}
	if err = q.Close(); err != nil {
		t.Fatal(err)
	}
	q, err = OpenQueue(path)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	if _, err = q.Generate("tenant:person-001", func(v int64) (*commonv1.EntityStateEvent, error) { return event(v), nil }); status.Code(err) != codes.ResourceExhausted {
		t.Fatal(err)
	}
	snapshot, err := q.Stats()
	if err != nil || snapshot.Pending != 1 {
		t.Fatal(snapshot, err)
	}
}

func TestInboxDerivedIndexesMigrateAndRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "inbox.db")
	inbox, err := OpenInbox(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"done", "running", "cancelled"} {
		if _, err = inbox.Accept(command(key, false)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = inbox.Accept(command("cancelled", true)); err != nil {
		t.Fatal(err)
	}
	if _, err = inbox.CommitResult("done", inspectionResult(command("done", false).Task)); err != nil {
		t.Fatal(err)
	}
	if err = inbox.db.Update(func(tx *bolt.Tx) error {
		v, err := getEntry(tx, "done")
		if err != nil {
			return err
		}
		v.Done = true
		if err = putEntry(tx, "done", v); err != nil {
			return err
		}
		// 模拟旧版文件：只移除可重建索引，保留完整命令、结果和幂等墓碑。
		for _, name := range []string{"inbox_pending", "inbox_active"} {
			if tx.Bucket([]byte(name)) != nil {
				if err = tx.DeleteBucket([]byte(name)); err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = inbox.Close(); err != nil {
		t.Fatal(err)
	}
	inbox, err = OpenInbox(path)
	if err != nil {
		t.Fatal(err)
	}
	defer inbox.Close()
	if err = inbox.db.View(func(tx *bolt.Tx) error {
		pending, active := tx.Bucket([]byte("inbox_pending")), tx.Bucket([]byte("inbox_active"))
		if pending == nil || active == nil {
			return errors.New("old inbox did not acquire derived indexes")
		}
		for _, key := range []string{"running", "cancelled"} {
			if pending.Get([]byte(key)) == nil {
				return fmt.Errorf("pending %s missing", key)
			}
		}
		if pending.Get([]byte("done")) != nil || active.Get([]byte("cancelled")) != nil || active.Get([]byte("running")) == nil {
			return errors.New("wrong migrated membership")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	rollback := errors.New("rollback")
	if err = inbox.db.Update(func(tx *bolt.Tx) error {
		v, err := getEntry(tx, "running")
		if err != nil {
			return err
		}
		v.Done = true
		if err = putEntry(tx, "running", v); err != nil {
			return err
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	keys, err := inbox.PendingKeys()
	if err != nil || len(keys) != 2 {
		t.Fatal(keys, err)
	}
	if _, err = inbox.AcceptWithLimit(command("overflow", false), 1, false); err != nil {
		t.Fatal(err)
	}
	overflow, err := inbox.Entry("overflow")
	if err != nil || !overflow.Rejected {
		t.Fatal(overflow, err)
	}
	if _, err = inbox.Accept(command("running", true)); err != nil {
		t.Fatal(err)
	}
	if _, err = inbox.AcceptWithLimit(command("replacement", false), 1, false); err != nil {
		t.Fatal(err)
	}
	replacement, err := inbox.Entry("replacement")
	if err != nil || replacement.Rejected {
		t.Fatal(replacement, err)
	}
	if fresh, err := inbox.Accept(command("done", false)); err != nil || fresh {
		t.Fatal("completed tombstone lost", fresh, err)
	}
	if count, err := inbox.EffectCount("done"); err != nil || count != 1 {
		t.Fatal(count, err)
	}
}

func BenchmarkInboxPendingWithCompletedHistory(b *testing.B) {
	for _, count := range []int{10, 10000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			inbox, err := OpenInbox(filepath.Join(b.TempDir(), "inbox.db"))
			if err != nil {
				b.Fatal(err)
			}
			defer inbox.Close()
			if err = inbox.db.Update(func(tx *bolt.Tx) error {
				for n := 0; n < count; n++ {
					c := command(fmt.Sprintf("done-%06d", n), false)
					raw, err := proto.Marshal(c)
					if err != nil {
						return err
					}
					if err = putEntry(tx, c.Task.ExecutionKey, &InboxEntry{Command: raw, Done: true, Phase: "terminal"}); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				b.Fatal(err)
			}
			if _, err = inbox.Accept(command("pending", false)); err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			b.ReportAllocs()
			for n := 0; n < b.N; n++ {
				keys, err := inbox.PendingKeys()
				if err != nil || len(keys) != 1 {
					b.Fatal(keys, err)
				}
			}
		})
	}
}
