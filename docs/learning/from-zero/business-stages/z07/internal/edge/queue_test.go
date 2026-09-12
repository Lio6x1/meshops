package edge

import (
	"bytes"
	"errors"
	commonv1 "example.com/meshops-course/gen/common/v1"
	bolt "go.etcd.io/bbolt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"os"
	"path/filepath"
	"testing"
)

func event(v int64) *commonv1.EntityStateEvent {
	return &commonv1.EntityStateEvent{TenantId: "tenant", EntityId: "person-001", EntityVersion: v, EventId: "event-" + string(rune('a'+v)), SourceId: "source", SourceGeneration: 1}
}
func generate(t *testing.T, q *Queue) int64 {
	t.Helper()
	seq, e := q.Generate("tenant:person-001", func(v int64) (*commonv1.EntityStateEvent, error) { return event(v), nil })
	if e != nil {
		t.Fatal(e)
	}
	return seq
}

// A06/A12：确认丢失后，保留完全相同的字节、epoch 和实体版本。
func TestQueueRestartAndActualSentACK(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.db")
	q, e := OpenQueue(path)
	if e != nil {
		t.Fatal(e)
	}
	epoch := q.Epoch()
	generate(t, q)
	generate(t, q)
	original, _ := q.Pending(100)
	if e = q.Ack(epoch, 1); e == nil {
		t.Fatal("accepted unsent ack")
	}
	if e = q.MarkSent(epoch, 1, 2); e != nil {
		t.Fatal(e)
	}
	if e = q.Ack("wrong", 1); e == nil {
		t.Fatal("accepted wrong epoch")
	}
	if e = q.Ack(epoch, 3); e == nil {
		t.Fatal("accepted beyond sent")
	}
	if e = q.Close(); e != nil {
		t.Fatal(e)
	}
	q, e = OpenQueue(path)
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	if q.Epoch() != epoch {
		t.Fatal("epoch changed")
	}
	replayed, _ := q.Pending(100)
	for n := range original {
		if !proto.Equal(original[n].Event, replayed[n].Event) {
			t.Fatal("payload changed")
		}
	}
	if e = q.Ack(epoch, 2); e == nil {
		t.Fatal("restart preserved non-durable sent ceiling")
	}
	if e = q.MarkSent(epoch, 1, 2); e != nil {
		t.Fatal(e)
	}
	if e = q.Ack(epoch, 1); e != nil {
		t.Fatal(e)
	}
	if e = q.Ack(epoch, 0); e == nil {
		t.Fatal("regressing ack accepted")
	}
	s, _ := q.Stats()
	if s.Pending != 1 || s.ConfirmedSequence != 1 {
		t.Fatal(s)
	}
	if generate(t, q) != 3 {
		t.Fatal("sequence reset")
	}
	p, _ := q.Pending(100)
	if p[1].Event.EntityVersion != 3 {
		t.Fatal("entity version reset")
	}
}

// A13：生成失败或容量不足时，必须回滚两个分配器。
func TestQueueCapacityAndGenerationRollback(t *testing.T) {
	q, e := OpenQueue(filepath.Join(t.TempDir(), "queue.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	q.maxEntries = 1
	generate(t, q)
	_, e = q.Generate("tenant:person-001", func(v int64) (*commonv1.EntityStateEvent, error) { return event(v), nil })
	if status.Code(e) != codes.ResourceExhausted {
		t.Fatal(e)
	}
	if e = q.MarkSent(q.Epoch(), 1, 1); e != nil {
		t.Fatal(e)
	}
	if e = q.Ack(q.Epoch(), 1); e != nil {
		t.Fatal(e)
	}
	_, e = q.Generate("tenant:person-001", func(v int64) (*commonv1.EntityStateEvent, error) { return nil, errors.New("adapter failed") })
	if e == nil {
		t.Fatal("accepted invalid build")
	}
	if generate(t, q) != 2 {
		t.Fatal("allocated version on failure")
	}
	p, _ := q.Pending(1)
	if p[0].Event.EntityVersion != 2 {
		t.Fatal(p)
	}
	q.maxBytes = 1
	q.maxEntries = 100
	if _, e = q.Enqueue(event(2)); status.Code(e) != codes.ResourceExhausted {
		t.Fatal(e)
	}
}
func TestQueueDuplicateDoesNotAllocateVersion(t *testing.T) {
	q, e := OpenQueue(filepath.Join(t.TempDir(), "queue.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	generate(t, q)
	if _, e = q.Enqueue(event(1)); e != nil {
		t.Fatal(e)
	}
	generate(t, q)
	p, _ := q.Pending(100)
	if len(p) != 3 || p[0].Event.EntityVersion != 1 || p[1].Event.EntityVersion != 1 || p[2].Event.EntityVersion != 2 {
		t.Fatal(p)
	}
}

// A13：诊断逻辑损坏时不重写原文件。
func TestQueueCorruptionPreservesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.db")
	q, e := OpenQueue(path)
	if e != nil {
		t.Fatal(e)
	}
	generate(t, q)
	generate(t, q)
	e = q.db.Update(func(tx *bolt.Tx) error { return tx.Bucket(pendingBucket).Delete(key64(1)) })
	if e != nil {
		t.Fatal(e)
	}
	q.Close()
	before, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if corrupt, e := OpenQueue(path); e == nil {
		corrupt.Close()
		t.Fatal("opened corrupt queue")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("corrupt file modified")
	}
}
func TestCompactionRetainsOriginalAndAllState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.db")
	q, e := OpenQueue(path)
	if e != nil {
		t.Fatal(e)
	}
	for n := 0; n < 100; n++ {
		generate(t, q)
	}
	epoch := q.Epoch()
	if e = q.MarkSent(epoch, 1, 100); e != nil {
		t.Fatal(e)
	}
	if e = q.Ack(epoch, 90); e != nil {
		t.Fatal(e)
	}
	before, _ := q.Pending(100)
	q.Close()
	backup, e := CompactQueue(path)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(backup); e != nil {
		t.Fatal(e)
	}
	q, e = OpenQueue(path)
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	s, _ := q.Stats()
	if s.Epoch != epoch || s.ConfirmedSequence != 90 || s.Pending != 10 {
		t.Fatal(s)
	}
	after, _ := q.Pending(100)
	for n := range before {
		if !proto.Equal(before[n].Event, after[n].Event) {
			t.Fatal("compaction altered payload")
		}
	}
	if generate(t, q) != 101 {
		t.Fatal("compaction lost allocator")
	}
}
