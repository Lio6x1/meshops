package edge

import (
	"encoding/json"
	"errors"
	commonv1 "example.com/meshops-course/gen/common/v1"
	executorv1 "example.com/meshops-course/gen/executor/v1"
	taskv1 "example.com/meshops-course/gen/task/v1"
	"fmt"
	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"os"
	"time"
)

var inboxBucket = []byte("inbox")
var reportsBucket = []byte("reports")
var resultsBucket = []byte("results")

type Inbox struct{ db *bolt.DB }
type InboxEntry struct {
	Command       []byte `json:"command"`
	CancelCommand []byte `json:"cancelCommand,omitempty"`
	Cancel        bool   `json:"cancel"`
	Rejected      bool   `json:"rejected"`
	Phase         string `json:"phase"`
	Acked         bool   `json:"acked"`
	Executing     bool   `json:"executing"`
	Done          bool   `json:"done"`
	PendingID     string `json:"pendingId,omitempty"`
	Outcome       int32  `json:"outcome"`
	Result        string `json:"result,omitempty"`
}
type durableResult struct {
	JSON        string `json:"json"`
	EffectCount int    `json:"effectCount"`
}

func OpenInbox(path string) (*Inbox, error) {
	info, statErr := os.Stat(path)
	fresh := os.IsNotExist(statErr)
	if statErr != nil && !fresh {
		return nil, statErr
	}
	if !fresh && info.Size() == 0 {
		return nil, errors.New("existing inbox is empty/corrupt; original file preserved")
	}
	db, err := openBolt(path)
	if err != nil {
		return nil, err
	}
	if fresh {
		err = db.Update(func(tx *bolt.Tx) error {
			for _, b := range [][]byte{inboxBucket, reportsBucket, resultsBucket} {
				if _, e := tx.CreateBucketIfNotExists(b); e != nil {
					return e
				}
			}
			return nil
		})
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	i := &Inbox{db}
	err = db.View(func(tx *bolt.Tx) error {
		for _, bucket := range [][]byte{inboxBucket, reportsBucket, resultsBucket} {
			if tx.Bucket(bucket) == nil {
				return errors.New("inbox missing durable bucket; original file preserved")
			}
		}
		if err := tx.Bucket(inboxBucket).ForEach(func(k, b []byte) error {
			var v InboxEntry
			if e := json.Unmarshal(b, &v); e != nil {
				return e
			}
			c := new(executorv1.ListenTasksResponse)
			if e := proto.Unmarshal(v.Command, c); e != nil {
				return e
			}
			if validCommand(c) != nil || c.Task.ExecutionKey != string(k) {
				return errors.New("invalid inbox execution key")
			}
			if v.PendingID != "" && tx.Bucket(reportsBucket).Get([]byte(v.PendingID)) == nil {
				return errors.New("missing durable report")
			}
			if v.Executing && !v.Acked {
				return errors.New("EXECUTING persisted without ACKED")
			}
			if v.Cancel {
				cancel := new(executorv1.ListenTasksResponse)
				if proto.Unmarshal(v.CancelCommand, cancel) != nil || validCommand(cancel) != nil || cancel.Kind != executorv1.TaskCommandKind_TASK_COMMAND_KIND_CANCEL || cancel.Task.ExecutionKey != string(k) {
					return errors.New("invalid cancellation tombstone")
				}
			}
			if v.Outcome == int32(commonv1.TaskStatus_TASK_STATUS_SUCCEEDED) {
				var result durableResult
				if json.Unmarshal(tx.Bucket(resultsBucket).Get(k), &result) != nil || result.EffectCount != 1 || result.JSON != v.Result || result.JSON != inspectionResult(c.Task) {
					return errors.New("terminal inbox/result mismatch")
				}
			}
			return nil
		}); err != nil {
			return err
		}
		if err := tx.Bucket(reportsBucket).ForEach(func(k, b []byte) error {
			r := new(taskv1.ReportTaskStatusRequest)
			if e := proto.Unmarshal(b, r); e != nil {
				return e
			}
			v, e := getEntry(tx, r.ExecutionKey)
			if e != nil {
				return e
			}
			if r.EventId != string(k) || v.PendingID != r.EventId {
				return errors.New("orphan or mismatched durable report")
			}
			return nil
		}); err != nil {
			return err
		}
		return tx.Bucket(resultsBucket).ForEach(func(k, b []byte) error {
			v, e := getEntry(tx, string(k))
			if e != nil {
				return e
			}
			var r durableResult
			if e = json.Unmarshal(b, &r); e != nil {
				return e
			}
			if r.EffectCount != 1 || r.JSON != v.Result || v.Outcome != int32(commonv1.TaskStatus_TASK_STATUS_SUCCEEDED) {
				return errors.New("orphan or mismatched durable effect")
			}
			return nil
		})
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	return i, nil
}
func (i *Inbox) Close() error { return i.db.Close() }
func (i *Inbox) BindExecutor(identity string) error {
	return i.db.Update(func(tx *bolt.Tx) error {
		b, e := tx.CreateBucketIfNotExists([]byte("identity"))
		if e != nil {
			return e
		}
		old := b.Get([]byte("executor"))
		if old != nil && string(old) != identity {
			return errors.New("inbox belongs to a different tenant/executor")
		}
		return b.Put([]byte("executor"), []byte(identity))
	})
}
func getEntry(tx *bolt.Tx, key string) (*InboxEntry, error) {
	b := tx.Bucket(inboxBucket).Get([]byte(key))
	if b == nil {
		return nil, errors.New("unknown execution key")
	}
	v := new(InboxEntry)
	return v, json.Unmarshal(b, v)
}
func putEntry(tx *bolt.Tx, key string, v *InboxEntry) error {
	b, e := json.Marshal(v)
	if e != nil {
		return e
	}
	return tx.Bucket(inboxBucket).Put([]byte(key), b)
}
func validCommand(c *executorv1.ListenTasksResponse) error {
	if c == nil || c.Task == nil || c.Task.TaskId == "" || c.Task.ExecutionKey != c.Task.TaskId || c.CommandId == "" || c.DispatchId == "" || c.Attempt < 1 {
		return errors.New("invalid command identity")
	}
	if c.Kind != executorv1.TaskCommandKind_TASK_COMMAND_KIND_CANCEL && c.Kind != executorv1.TaskCommandKind_TASK_COMMAND_KIND_EXECUTE {
		return errors.New("invalid command kind")
	}
	suffix := "-execute"
	if c.Kind == executorv1.TaskCommandKind_TASK_COMMAND_KIND_CANCEL {
		suffix = "-cancel"
	}
	if c.CommandId != c.Task.TaskId+suffix || c.DispatchId != fmt.Sprintf("%s-%d", c.CommandId, c.Attempt) {
		return errors.New("command/dispatch identity is not deterministic")
	}
	return nil
}
func (i *Inbox) Accept(c *executorv1.ListenTasksResponse) (bool, error) {
	return i.AcceptWithLimit(c, 4, false)
}

// Cancellation is committed before the caller signals a running timer. The same
// write transaction serializes it against CommitResult, including after restart.
func (i *Inbox) AcceptWithLimit(c *executorv1.ListenTasksResponse, capacity int, reject bool) (bool, error) {
	if capacity < 1 || capacity > 4 {
		return false, errors.New("executor capacity must be 1..4")
	}
	if e := validCommand(c); e != nil {
		return false, e
	}
	encoded, e := proto.Marshal(c)
	if e != nil {
		return false, e
	}
	fresh := false
	e = i.db.Update(func(tx *bolt.Tx) error {
		key := c.Task.ExecutionKey
		b := tx.Bucket(inboxBucket)
		v := new(InboxEntry)
		if old := b.Get([]byte(key)); old != nil {
			if e := json.Unmarshal(old, v); e != nil {
				return e
			}
			previous := new(executorv1.ListenTasksResponse)
			if e := proto.Unmarshal(v.Command, previous); e != nil {
				return e
			}
			if previous.Task.TargetEntityId != c.Task.TargetEntityId || previous.Task.ExecutorId != c.Task.ExecutorId || previous.Task.TenantId != c.Task.TenantId {
				return errors.New("command binding changed")
			}
			if c.Kind == executorv1.TaskCommandKind_TASK_COMMAND_KIND_EXECUTE {
				return nil
			}
			if v.Cancel {
				return nil
			}
			fresh = true
		} else {
			fresh = true
			v.Command = encoded
			v.Phase = "accepted"
			if c.Kind == executorv1.TaskCommandKind_TASK_COMMAND_KIND_EXECUTE {
				active := 0
				if e := b.ForEach(func(_, raw []byte) error {
					var x InboxEntry
					if e := json.Unmarshal(raw, &x); e != nil {
						return e
					}
					if !x.Done && !x.Cancel && !x.Rejected && x.Outcome == 0 {
						active++
					}
					return nil
				}); e != nil {
					return e
				}
				v.Rejected = reject || active >= capacity
				if v.Rejected {
					v.Outcome = int32(commonv1.TaskStatus_TASK_STATUS_REJECTED)
					v.Phase = "terminal"
				}
			}
		}
		if c.Kind == executorv1.TaskCommandKind_TASK_COMMAND_KIND_CANCEL {
			v.Cancel = true
			v.CancelCommand = encoded
			v.Done = false
			if v.Outcome == 0 || v.Rejected {
				v.Outcome = int32(commonv1.TaskStatus_TASK_STATUS_CANCELLED)
				v.Phase = "terminal"
			}
		}
		return putEntry(tx, key, v)
	})
	return fresh, e
}
func (i *Inbox) Entry(key string) (*InboxEntry, error) {
	var v *InboxEntry
	err := i.db.View(func(tx *bolt.Tx) error { var e error; v, e = getEntry(tx, key); return e })
	return v, err
}
func (i *Inbox) PendingKeys() ([]string, error) {
	var keys []string
	err := i.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(inboxBucket).ForEach(func(k, b []byte) error {
			var v InboxEntry
			if e := json.Unmarshal(b, &v); e != nil {
				return e
			}
			if !v.Done {
				keys = append(keys, string(k))
			}
			return nil
		})
	})
	return keys, err
}
func (i *Inbox) CommitResult(key, result string) (bool, error) {
	committed := false
	err := i.db.Update(func(tx *bolt.Tx) error {
		v, e := getEntry(tx, key)
		if e != nil {
			return e
		}
		if v.Cancel || v.Outcome != 0 {
			return nil
		}
		c := new(executorv1.ListenTasksResponse)
		if e = proto.Unmarshal(v.Command, c); e != nil {
			return e
		}
		expected := inspectionResult(c.Task)
		if result != expected {
			return errors.New("result differs from deterministic inspection result")
		}
		if tx.Bucket(resultsBucket).Get([]byte(key)) != nil {
			return errors.New("result exists without terminal inbox")
		}
		b, e := json.Marshal(durableResult{result, 1})
		if e != nil {
			return e
		}
		if e = tx.Bucket(resultsBucket).Put([]byte(key), b); e != nil {
			return e
		}
		v.Outcome = int32(commonv1.TaskStatus_TASK_STATUS_SUCCEEDED)
		v.Result = result
		v.Phase = "terminal"
		committed = true
		return putEntry(tx, key, v)
	})
	return committed, err
}
func inspectionResult(t *commonv1.Task) string {
	b, _ := json.Marshal(struct {
		InspectionID string `json:"inspection_id"`
		EntityID     string `json:"entity_id"`
		Outcome      string `json:"outcome"`
		EffectCount  int    `json:"effect_count"`
	}{t.TaskId, t.TargetEntityId, "ok", 1})
	return string(b)
}
func (i *Inbox) Fail(key string) error {
	return i.db.Update(func(tx *bolt.Tx) error {
		v, e := getEntry(tx, key)
		if e != nil {
			return e
		}
		if v.Cancel || v.Outcome != 0 {
			return nil
		}
		v.Outcome = int32(commonv1.TaskStatus_TASK_STATUS_FAILED)
		v.Phase = "terminal"
		return putEntry(tx, key, v)
	})
}
func (i *Inbox) EffectCount(key string) (int, error) {
	n := 0
	err := i.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket(resultsBucket).Get([]byte(key))
		if b == nil {
			return nil
		}
		var r durableResult
		if e := json.Unmarshal(b, &r); e != nil {
			return e
		}
		n = r.EffectCount
		return nil
	})
	return n, err
}
func (i *Inbox) Stats() (accepted, completed, effects int, err error) {
	err = i.db.View(func(tx *bolt.Tx) error {
		if e := tx.Bucket(inboxBucket).ForEach(func(_, b []byte) error {
			var v InboxEntry
			if e := json.Unmarshal(b, &v); e != nil {
				return e
			}
			if !v.Rejected {
				accepted++
			}
			if v.Done {
				completed++
			}
			return nil
		}); e != nil {
			return e
		}
		return tx.Bucket(resultsBucket).ForEach(func(_, b []byte) error {
			var r durableResult
			if e := json.Unmarshal(b, &r); e != nil {
				return e
			}
			effects += r.EffectCount
			return nil
		})
	})
	return
}
func (i *Inbox) StoreReport(key string, r *taskv1.ReportTaskStatusRequest) error {
	return i.db.Update(func(tx *bolt.Tx) error {
		v, e := getEntry(tx, key)
		if e != nil {
			return e
		}
		if v.PendingID != "" {
			return errors.New("report already pending")
		}
		b, e := proto.Marshal(r)
		if e != nil {
			return e
		}
		if e = tx.Bucket(reportsBucket).Put([]byte(r.EventId), b); e != nil {
			return e
		}
		v.PendingID = r.EventId
		return putEntry(tx, key, v)
	})
}
func (i *Inbox) Report(key string) (*taskv1.ReportTaskStatusRequest, error) {
	var r *taskv1.ReportTaskStatusRequest
	err := i.db.View(func(tx *bolt.Tx) error {
		v, e := getEntry(tx, key)
		if e != nil || v.PendingID == "" {
			return e
		}
		r = new(taskv1.ReportTaskStatusRequest)
		return proto.Unmarshal(tx.Bucket(reportsBucket).Get([]byte(v.PendingID)), r)
	})
	return r, err
}

// RetireRejectedReport requires an explicit FAILED_PRECONDITION response for
// this report followed by an authoritative GetTask. It does not resolve an
// uncertain delivery, nor discard a locally committed result or cancellation.
func (i *Inbox) RetireRejectedReport(key string, r *taskv1.ReportTaskStatusRequest, current *commonv1.Task) error {
	if r == nil || (r.Status != commonv1.TaskStatus_TASK_STATUS_ACKED && r.Status != commonv1.TaskStatus_TASK_STATUS_EXECUTING) || current == nil || current.Status < commonv1.TaskStatus_TASK_STATUS_SUCCEEDED || current.Status > commonv1.TaskStatus_TASK_STATUS_REJECTED {
		return status.Error(codes.FailedPrecondition, "only a rejected nonterminal report against a terminal task can be retired")
	}
	return i.db.Update(func(tx *bolt.Tx) error {
		v, err := getEntry(tx, key)
		if err != nil {
			return err
		}
		c := new(executorv1.ListenTasksResponse)
		if err = proto.Unmarshal(v.Command, c); err != nil {
			return err
		}
		if c.Task == nil || key != c.Task.ExecutionKey || r.TaskId != c.Task.TaskId || r.ExecutionKey != key || r.ExecutorId != c.Task.ExecutorId || current.TaskId != c.Task.TaskId || current.ExecutionKey != key || current.ExecutorId != c.Task.ExecutorId || current.TenantId != c.Task.TenantId || current.TargetEntityId != c.Task.TargetEntityId {
			return status.Error(codes.PermissionDenied, "terminal task does not match durable execution identity")
		}
		stored := new(taskv1.ReportTaskStatusRequest)
		if v.PendingID != r.EventId || proto.Unmarshal(tx.Bucket(reportsBucket).Get([]byte(v.PendingID)), stored) != nil || !proto.Equal(stored, r) {
			return errors.New("rejected report identity changed")
		}
		if err = tx.Bucket(reportsBucket).Delete([]byte(r.EventId)); err != nil {
			return err
		}
		v.PendingID = ""
		if v.Outcome == 0 {
			v.Done = true
			v.Phase = "terminal"
		}
		return putEntry(tx, key, v)
	})
}

// ResolveReport discards identity only after a received response, or an explicit
// ABORTED response which guarantees the server did not accept this report.
func (i *Inbox) ResolveReport(key string, r *taskv1.ReportTaskStatusRequest, aborted bool) error {
	return i.db.Update(func(tx *bolt.Tx) error {
		v, e := getEntry(tx, key)
		if e != nil {
			return e
		}
		if v.PendingID != r.EventId {
			return errors.New("pending report identity changed")
		}
		if e = tx.Bucket(reportsBucket).Delete([]byte(r.EventId)); e != nil {
			return e
		}
		v.PendingID = ""
		if !aborted {
			switch r.Status {
			case commonv1.TaskStatus_TASK_STATUS_ACKED:
				v.Acked = true
			case commonv1.TaskStatus_TASK_STATUS_EXECUTING:
				v.Executing = true
				v.Phase = "running"
			default:
				v.Done = true
			}
		}
		return putEntry(tx, key, v)
	})
}
func decodeCommand(v *InboxEntry) (*executorv1.ListenTasksResponse, error) {
	c := new(executorv1.ListenTasksResponse)
	b := v.Command
	if v.Cancel && len(v.CancelCommand) > 0 && commonv1.TaskStatus(v.Outcome) == commonv1.TaskStatus_TASK_STATUS_CANCELLED {
		b = v.CancelCommand
	}
	if e := proto.Unmarshal(b, c); e != nil {
		return nil, e
	}
	return c, nil
}
func inspectDuration(t *commonv1.Task) (time.Duration, error) {
	if t.TaskType != "inspect" || t.Payload == nil {
		return 0, errors.New("only inspect is supported")
	}
	var v struct {
		DurationSeconds int64  `json:"duration_seconds"`
		Note            string `json:"note"`
	}
	if e := json.Unmarshal([]byte(t.Payload.PayloadJson), &v); e != nil {
		return 0, e
	}
	if v.DurationSeconds < 1 || v.DurationSeconds > 60 {
		return 0, fmt.Errorf("duration_seconds outside 1..60")
	}
	return time.Duration(v.DurationSeconds) * time.Second, nil
}
