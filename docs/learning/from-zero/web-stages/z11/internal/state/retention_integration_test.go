package state

import (
	"context"
	"example.com/meshops-course/internal/platform"
	"fmt"
	"os"
	"testing"
	"time"
)

func TestHistoryRetentionCatchesUpMoreThanOneBatch(t *testing.T) {
	if os.Getenv("MESHOPS_TEST_MYSQL_DSN") == "" {
		t.Skip("real MySQL required")
	}
	db, err := platform.OpenDB("MESHOPS_TEST_MYSQL_DSN")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tenant := "retention_" + newID()
	old := time.Now().UTC().Add(-8 * 24 * time.Hour)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	for i := 0; i < 1500; i++ {
		event := fmt.Sprintf("e%d", i)
		r, e := tx.ExecContext(ctx, `INSERT INTO entity_history_samples(tenant_id,entity_id,occurred_at,sampled_at,event_id,source_id,entity_version,snapshot,sample_reason) VALUES(?,'p',?,?,?,'s',1,'{}','periodic')`, tenant, old, old, event)
		if e != nil {
			t.Fatal(e)
		}
		id, _ := r.LastInsertId()
		if _, e = tx.ExecContext(ctx, `INSERT INTO history_sample_keys(tenant_id,source_id,source_generation,event_id,sampled_at,sample_id) VALUES(?,'s',1,?,?,?)`, tenant, event, old, id); e != nil {
			t.Fatal(e)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	defer db.Exec(`DELETE FROM history_sample_keys WHERE tenant_id=?`, tenant)
	defer db.Exec(`DELETE FROM entity_history_samples WHERE tenant_id=?`, tenant)
	entity := &Entity{db: db}
	began := time.Now()
	if err = entity.pruneHistoryBatch(ctx, time.Now().UTC().Add(-7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow(`SELECT COUNT(*) FROM entity_history_samples WHERE tenant_id=?`, tenant).Scan(&count); err != nil || count != 0 {
		t.Fatalf("one maintenance pass left %d expired samples: %v", count, err)
	}
	if err = db.QueryRow(`SELECT COUNT(*) FROM history_sample_keys WHERE tenant_id=?`, tenant).Scan(&count); err != nil || count != 0 {
		t.Fatalf("dedup keys left: %d %v", count, err)
	}
	t.Logf("removed 1500 samples and their keys in %s", time.Since(began))
}

func TestHistoryRetentionBothClocksAndCutoff(t *testing.T) {
	if os.Getenv("MESHOPS_TEST_MYSQL_DSN") == "" {
		t.Skip("real MySQL required")
	}
	db, err := platform.OpenDB("MESHOPS_TEST_MYSQL_DSN")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	tenant := "retention_clocks_" + newID()
	defer db.Exec(`DELETE FROM history_sample_keys WHERE tenant_id=?`, tenant)
	defer db.Exec(`DELETE FROM entity_history_samples WHERE tenant_id=?`, tenant)
	cutoff := time.Now().UTC().Add(-7 * 24 * time.Hour).Truncate(time.Microsecond)
	old, recent := cutoff.Add(-time.Second), cutoff.Add(time.Hour)
	for _, row := range []struct {
		id                string
		occurred, sampled time.Time
	}{
		{"old_sample", recent, old}, {"old_occurred", old, recent}, {"both_old", old, old},
		{"recent", recent, recent}, {"exact_cutoff", cutoff, cutoff},
	} {
		result, err := db.ExecContext(ctx, `INSERT INTO entity_history_samples(tenant_id,entity_id,occurred_at,sampled_at,event_id,source_id,entity_version,snapshot,sample_reason) VALUES(?,'p',?,?,?,'s',1,'{}','periodic')`, tenant, row.occurred, row.sampled, row.id)
		if err != nil {
			t.Fatal(err)
		}
		id, err := result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.ExecContext(ctx, `INSERT INTO history_sample_keys(tenant_id,source_id,source_generation,event_id,sampled_at,sample_id) VALUES(?,'s',1,?,?,?)`, tenant, row.id, row.sampled, id); err != nil {
			t.Fatal(err)
		}
	}
	e := &Entity{db: db}
	if err = e.pruneHistoryBatch(ctx, cutoff); err != nil {
		t.Fatal(err)
	}
	var retained, keys int
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM entity_history_samples WHERE tenant_id=? AND event_id IN ('recent','exact_cutoff')`, tenant).Scan(&retained); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, `SELECT COUNT(*) FROM history_sample_keys WHERE tenant_id=?`, tenant).Scan(&keys); err != nil {
		t.Fatal(err)
	}
	if retained != 2 || keys != 2 {
		t.Fatalf("age union/cutoff or dedup pairing changed: samples=%d keys=%d", retained, keys)
	}
}
