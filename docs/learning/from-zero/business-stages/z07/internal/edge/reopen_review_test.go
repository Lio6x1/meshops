package edge

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	commonv1 "example.com/meshops-course/gen/common/v1"
	taskv1 "example.com/meshops-course/gen/task/v1"
	bolt "go.etcd.io/bbolt"
	"google.golang.org/protobuf/proto"
)

// 模拟旧版本留下的非法事实，不能让索引升级把待确认报告悄悄隐藏。
func TestInboxReopenRejectsUnschedulableFactsWithoutChangingFile(t *testing.T) {
	for _, kind := range []string{"done-with-report", "invalid-execute-payload"} {
		t.Run(kind, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "inbox.db")
			i, err := OpenInbox(path)
			if err != nil {
				t.Fatal(err)
			}
			c := command("task", false)
			if _, err = i.Accept(c); err != nil {
				t.Fatal(err)
			}
			if kind == "done-with-report" {
				err = i.StoreReport("task", &taskv1.ReportTaskStatusRequest{EventId: "pending", TaskId: "task", ExecutionKey: "task", Status: commonv1.TaskStatus_TASK_STATUS_ACKED})
				if err != nil {
					t.Fatal(err)
				}
			}
			err = i.db.Update(func(tx *bolt.Tx) error {
				v, err := getEntry(tx, "task")
				if err != nil {
					return err
				}
				if kind == "done-with-report" {
					v.Done = true
				} else {
					c.Task.Payload.PayloadJson = `{"duration_seconds":0}`
					v.Command, err = proto.Marshal(c)
					if err != nil {
						return err
					}
				}
				raw, err := json.Marshal(v)
				if err != nil {
					return err
				}
				return tx.Bucket(inboxBucket).Put([]byte("task"), raw)
			})
			if err != nil {
				t.Fatal(err)
			}
			if err = i.Close(); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			reopened, err := OpenInbox(path)
			if err == nil {
				reopened.Close()
				t.Fatal("inconsistent durable fact accepted")
			}
			after, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("failed validation changed original file", err)
			}
		})
	}
}
