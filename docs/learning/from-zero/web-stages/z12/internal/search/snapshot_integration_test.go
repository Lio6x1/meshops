//go:build integration

package search

import (
	"context"
	"database/sql"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

type snapshotIndex struct{ documents []Document }

func (s *snapshotIndex) Put(_ context.Context, d Document) (ApplyResult, error) {
	s.documents = append(s.documents, d)
	return Applied, nil
}

func TestSnapshotEstablishesReadViewBeforeReleasingLock(t *testing.T) {
	dsn := os.Getenv("MESHOPS_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("MESHOPS_TEST_MYSQL_DSN required")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatal("invalid test DSN")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	name := "search_snapshot_test_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if _, err = db.ExecContext(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if _, e := db.ExecContext(c, "DROP DATABASE "+name); e != nil {
			t.Error(e)
		}
	})
	cfg.DBName = name
	snapshotDB, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	defer snapshotDB.Close()
	_, err = snapshotDB.ExecContext(ctx, `CREATE TABLE tasks(tenant_id VARCHAR(64),task_id VARCHAR(128),target_entity_id VARCHAR(128),task_type VARCHAR(64),status VARCHAR(32),status_version INT,payload JSON,created_at TIMESTAMP(6),updated_at TIMESTAMP(6),cancelled_reason TEXT,failure_reason TEXT) ENGINE=InnoDB`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = snapshotDB.ExecContext(ctx, `INSERT INTO tasks VALUES('tenant-a','task-1','drone-001','inspect','DISPATCH_PENDING',1,'{"duration_seconds":1,"note":"before"}',UTC_TIMESTAMP(6),UTC_TIMESTAMP(6),NULL,NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	index := &snapshotIndex{}
	var point BinlogPosition
	err = Snapshot(ctx, snapshotDB, index, func(p BinlogPosition) error {
		point = p
		// 此次写入必须完成：全局锁此时已释放。
		_, e := snapshotDB.ExecContext(ctx, `UPDATE tasks SET status='ACKED',status_version=2,payload='{"duration_seconds":1,"note":"after"}'`)
		return e
	})
	if err != nil {
		t.Fatal(err)
	}
	if point.File == "" || point.Offset < 4 {
		t.Fatalf("missing binlog point: %+v", point)
	}
	if len(index.documents) != 1 || index.documents[0].Note != "before" || index.documents[0].StatusVersion != 1 {
		t.Fatalf("snapshot read view moved: %+v", index.documents)
	}
}
