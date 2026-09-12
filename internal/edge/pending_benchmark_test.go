package edge

import (
	"fmt"
	"path/filepath"
	"testing"

	commonv1 "example.com/meshops-course/gen/common/v1"
	bolt "go.etcd.io/bbolt"
	"google.golang.org/protobuf/proto"
)

func backlogInbox(b *testing.B, count int) *Inbox {
	b.Helper()
	i, err := OpenInbox(filepath.Join(b.TempDir(), "inbox.db"))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { i.Close() })
	if err = i.db.Update(func(tx *bolt.Tx) error {
		for n := 0; n < count; n++ {
			c := command(fmt.Sprintf("pending-%06d", n), false)
			raw, e := proto.Marshal(c)
			if e != nil {
				return e
			}
			if e = putEntry(tx, c.Task.ExecutionKey, &InboxEntry{Command: raw, Rejected: true, Outcome: int32(commonv1.TaskStatus_TASK_STATUS_REJECTED), Phase: "terminal"}); e != nil {
				return e
			}
		}
		return nil
	}); err != nil {
		b.Fatal(err)
	}
	return i
}

func BenchmarkInboxBacklogFullScan(b *testing.B) {
	for _, count := range []int{10, 10000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			i := backlogInbox(b, count)
			b.ReportAllocs()
			b.ResetTimer()
			for n := 0; n < b.N; n++ {
				keys, err := i.PendingKeys()
				if err != nil || len(keys) != count {
					b.Fatal(len(keys), err)
				}
			}
		})
	}
}

// Page measures one scheduler read (maximum worker count four => eight keys),
// not the time to consume an entire backlog or perform remote task execution.
func BenchmarkInboxBacklogPage(b *testing.B) {
	for _, count := range []int{10, 10000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			i := backlogInbox(b, count)
			through, err := i.pendingBoundary()
			if err != nil {
				b.Fatal(err)
			}
			after := ""
			b.ReportAllocs()
			b.ResetTimer()
			for n := 0; n < b.N; n++ {
				keys, err := i.pendingPage(after, through, 8)
				if err != nil || len(keys) > 8 {
					b.Fatal(len(keys), err)
				}
				if len(keys) == 0 {
					after = ""
				} else {
					after = keys[len(keys)-1]
				}
			}
		})
	}
}
