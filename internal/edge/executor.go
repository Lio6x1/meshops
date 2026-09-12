package edge

import (
	"context"
	"errors"
	commonv1 "example.com/meshops-course/gen/common/v1"
	executorv1 "example.com/meshops-course/gen/executor/v1"
	taskv1 "example.com/meshops-course/gen/task/v1"
	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

type ExecutorStats struct {
	DuplicateCommands atomic.Int64
	InvalidCommands   atomic.Int64
}

func terminal(s commonv1.TaskStatus) bool { return s >= commonv1.TaskStatus_TASK_STATUS_SUCCEEDED }
func retryable(err error) bool {
	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded, codes.Canceled, codes.ResourceExhausted, codes.Unknown:
		return true
	}
	return false
}

// RunExecutor uses a bounded pool, and an independent listener so cancellation
// can durably overtake a worker's timer or result transaction.
func RunExecutor(ctx context.Context, inbox *Inbox, commands executorv1.ExecutorServiceClient, tasks taskv1.TaskServiceClient, executor, mode string, concurrency int, stats *ExecutorStats) error {
	if concurrency < 1 || concurrency > 4 {
		return errors.New("concurrency must be 1..4")
	}
	if mode != "normal" && mode != "fail" && mode != "reject" {
		return errors.New("mode must be normal, fail, or reject")
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	errCh := make(chan error, concurrency+1)
	var wg sync.WaitGroup
	var mu sync.Mutex
	active := map[string]bool{}
	// Notifications are hints, not the queue: bbolt remains authoritative. One
	// buffered wake coalesces command bursts and worker completions; startup and
	// the slow fallback recover work even when no new command arrives.
	wake := make(chan struct{}, 1)
	notify := func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for attempt := 0; ; attempt++ {
			if runCtx.Err() != nil {
				return
			}
			stream, e := commands.ListenTasks(runCtx, &executorv1.ListenTasksRequest{ExecutorId: executor})
			if e == nil {
				for {
					cmd, recvErr := stream.Recv()
					if recvErr != nil {
						e = recvErr
						break
					}
					var fresh bool
					var acceptErr error
					if cmd.GetTask().GetExecutorId() != executor {
						acceptErr = errInvalidCommand
					} else {
						fresh, acceptErr = inbox.AcceptWithLimit(cmd, concurrency, mode == "reject")
					}
					if acceptErr != nil {
						// 单个非法命令不能取消其他已持久化任务；只隔离明确的输入错误。
						// 磁盘/事务错误意味着没有可靠接收，必须退出，不能假装接收成功。
						if errors.Is(acceptErr, errInvalidCommand) {
							stats.InvalidCommands.Add(1)
							slog.Warn("executor ignored invalid command", "executor", executor, "error", acceptErr)
							continue
						}
						errCh <- acceptErr
						return
					}
					if !fresh {
						stats.DuplicateCommands.Add(1)
					}
					attempt = 0
					notify()
				}
			}
			if runCtx.Err() != nil {
				return
			}
			if e != nil && !retryable(e) {
				errCh <- e
				return
			}
			if pause(runCtx, reconnectDelay(attempt)) != nil {
				return
			}
		}
	}()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	notify()
	after := ""
	through := ""
	var result error
loop:
	for {
		select {
		case <-ctx.Done():
			result = ctx.Err()
			break loop
		case result = <-errCh:
			break loop
		case <-tick.C:
		case <-wake:
		}
		mu.Lock()
		full := len(active) >= concurrency
		mu.Unlock()
		if full {
			continue
		}
		// At most two small pages (including a wrap) per wake. Advance after
		// each examined key, not the copied page's end: unused entries must not
		// be skipped when the pool fills midway through a page.
		pageSize := concurrency * 2
		var e error
		if through == "" {
			through, e = inbox.pendingBoundary()
			if e != nil {
				result = e
				break loop
			}
		}
		keys, e := inbox.pendingPage(after, through, pageSize)
		if e == nil && len(keys) == 0 && after != "" {
			after = ""
			through, e = inbox.pendingBoundary()
			if e == nil {
				keys, e = inbox.pendingPage(after, through, pageSize)
			}
		}
		if e != nil {
			result = e
			break loop
		}
		examined := 0
		for _, key := range keys {
			mu.Lock()
			if len(active) >= concurrency {
				mu.Unlock()
				break
			}
			after = key
			examined++
			if active[key] {
				mu.Unlock()
				continue
			}
			active[key] = true
			mu.Unlock()
			wg.Add(1)
			go func(key string) {
				defer wg.Done()
				defer func() { mu.Lock(); delete(active, key); mu.Unlock(); notify() }()
				if e := executeOne(runCtx, inbox, tasks, key, mode); e != nil && runCtx.Err() == nil {
					select {
					case errCh <- e:
					case <-runCtx.Done():
					}
				}
			}(key)
		}
		mu.Lock()
		spare := len(active) < concurrency
		mu.Unlock()
		if spare {
			// A worker may finish between the capacity break and this check.
			// Continue the same page/round then; resetting its cursor would let
			// a stream of lower keys repeatedly jump ahead of its unread tail.
			if examined < len(keys) || len(keys) == pageSize {
				notify()
			} else {
				after = ""
				through = ""
			}
		}
	}
	cancel()
	wg.Wait()
	return result
}
func executeOne(ctx context.Context, inbox *Inbox, tasks taskv1.TaskServiceClient, key, mode string) error {
	for {
		if e := ctx.Err(); e != nil {
			return e
		}
		v, e := inbox.Entry(key)
		if e != nil {
			return e
		}
		if v.Done {
			return nil
		}
		pending, e := inbox.Report(key)
		if e != nil {
			return e
		}
		if pending != nil {
			rpcCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			_, e = tasks.ReportTaskStatus(rpcCtx, pending)
			cancel()
			if e == nil {
				if e = inbox.ResolveReport(key, pending, false); e != nil {
					return e
				}
				continue
			}
			if status.Code(e) == codes.Aborted {
				if e = inbox.ResolveReport(key, pending, true); e != nil {
					return e
				}
				continue
			}
			if status.Code(e) == codes.FailedPrecondition && (pending.Status == commonv1.TaskStatus_TASK_STATUS_ACKED || pending.Status == commonv1.TaskStatus_TASK_STATUS_EXECUTING) {
				// This explicit rejection proves the pending nonterminal report was
				// not accepted: Task checks duplicate receipts before its terminal
				// barrier. Confirm the authoritative identity and terminal state
				// before retiring it; an unavailable reconciliation keeps it intact.
				rpcCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				current, queryErr := tasks.GetTask(rpcCtx, &taskv1.GetTaskRequest{TaskId: pending.TaskId})
				cancel()
				if queryErr == nil {
					queryErr = inbox.RetireRejectedReport(key, pending, current.GetTask())
				}
				if queryErr == nil {
					continue
				}
				e = queryErr
			}
			if !retryable(e) {
				return e
			}
			if e = pause(ctx, time.Second); e != nil {
				return e
			}
			continue
		}
		command, e := decodeCommand(v)
		if e != nil {
			return e
		}
		rpcCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		current, e := tasks.GetTask(rpcCtx, &taskv1.GetTaskRequest{TaskId: command.Task.TaskId})
		cancel()
		if e != nil {
			if !retryable(e) {
				return e
			}
			if e = pause(ctx, time.Second); e != nil {
				return e
			}
			continue
		}
		if current.Task == nil {
			return errors.New("GetTask returned no task")
		}
		// Re-read local cancellation after the network call, before selecting a report.
		v, e = inbox.Entry(key)
		if e != nil {
			return e
		}
		command, e = decodeCommand(v)
		if e != nil {
			return e
		}
		if terminal(current.Task.Status) && v.Outcome == 0 {
			return inbox.db.Update(func(tx *bolt.Tx) error {
				x, e := getEntry(tx, key)
				if e != nil {
					return e
				}
				if x.Outcome == 0 {
					x.Done = true
					x.Phase = "terminal"
				}
				return putEntry(tx, key, x)
			})
		}
		desired := commonv1.TaskStatus(v.Outcome)
		if desired == commonv1.TaskStatus_TASK_STATUS_UNSPECIFIED {
			if current.Task.CancelRequested {
				if e = pause(ctx, 100*time.Millisecond); e != nil {
					return e
				}
				continue
			}
			if !v.Acked {
				desired = commonv1.TaskStatus_TASK_STATUS_ACKED
			} else if !v.Executing {
				desired = commonv1.TaskStatus_TASK_STATUS_EXECUTING
			} else {
				duration, e := inspectDuration(command.Task)
				if e != nil {
					return e
				}
				deadline := time.Now().Add(duration)
				for time.Now().Before(deadline) {
					v, e = inbox.Entry(key)
					if e != nil {
						return e
					}
					if v.Cancel {
						break
					}
					if e = pause(ctx, 50*time.Millisecond); e != nil {
						return e
					}
				}
				if mode == "fail" {
					e = inbox.Fail(key)
				} else {
					_, e = inbox.CommitResult(key, inspectionResult(command.Task))
				}
				if e != nil {
					return e
				}
				continue
			}
		}
		reason := ""
		if desired == commonv1.TaskStatus_TASK_STATUS_REJECTED {
			reason = "executor unavailable or deterministic reject mode"
		}
		if desired == commonv1.TaskStatus_TASK_STATUS_FAILED {
			reason = "deterministic fail mode"
		}
		if desired == commonv1.TaskStatus_TASK_STATUS_CANCELLED {
			reason = "cancellation persisted by executor"
		}
		report := &taskv1.ReportTaskStatusRequest{EventId: randomID(), TaskId: command.Task.TaskId, ExecutionKey: key, ExecutorId: command.Task.ExecutorId, DispatchId: command.DispatchId, ExpectedStatusVersion: current.Task.StatusVersion, Status: desired, ResultJson: v.Result, Reason: reason, OccurredAt: timestamppb.Now()}
		if e = inbox.StoreReport(key, report); e != nil {
			return e
		}
	}
}
