//go:build integration

package tasks

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"example.com/meshops-course/internal/platform"
)

func batchRegistry(n int) *platform.Registry {
	r := &platform.Registry{Bindings: map[string]platform.Binding{}, Sources: map[string]platform.Source{}, Credentials: map[string]string{"batch:source": "batch-only-machine-credential"}}
	s := platform.Source{TenantID: "batch", ID: "source", Adapter: "drone", Generation: 1, Rate: 100, Entities: map[string]string{}}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("entity-%06d", i)
		s.Entities[id] = id
		r.Bindings[platform.Key("batch", id)] = platform.Binding{TenantID: "batch", EntityID: id, Type: "drone", SourceID: "source", SourceGeneration: 1, ExecutorID: "executor", Tasks: []string{"move", "inspect"}}
	}
	r.Sources["batch:source"] = s
	return r
}

// 使用专属连接的服务端计数，避免并行测试或驱动预处理影响业务 SQL 次数。
func bindingSQLCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	rows, err := db.Query(`SHOW SESSION STATUS WHERE Variable_name IN ('Com_select','Com_insert','Com_update')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var name string
		var count int
		if err := rows.Scan(&name, &count); err != nil {
			t.Fatal(err)
		}
		n += count
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestBindingScaleRoundTrips(t *testing.T) {
	for _, n := range []int{1000, 10000} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			db := isolatedDB(t)
			db.SetMaxOpenConns(1)
			ctx := context.Background()
			if err := Migrate(ctx, db, "../../migrations"); err != nil {
				t.Fatal(err)
			}
			r := batchRegistry(n)
			for _, operation := range []struct {
				name string
				run  func(context.Context, *sql.DB, *platform.Registry) error
			}{{"seed", Seed}, {"check", CheckBindings}} {
				before := bindingSQLCount(t, db)
				start := time.Now()
				if err := operation.run(ctx, db, r); err != nil {
					t.Fatal(err)
				}
				elapsed := time.Since(start)
				count := bindingSQLCount(t, db) - before
				t.Logf("entities=%d operation=%s elapsed=%s SQL=%d", n, operation.name, elapsed, count)
				if count > n/10+20 {
					t.Errorf("per-entity round trips remain: %d SQL for %d entities", count, n)
				}
			}
			var count int
			if err := db.QueryRow(`SELECT COUNT(*) FROM entities`).Scan(&count); err != nil || count != n {
				t.Fatalf("entity count=%d want=%d error=%v", count, n, err)
			}
		})
	}
}

func TestBindingBatchesRejectConflictsAndRollback(t *testing.T) {
	ctx := context.Background()
	db := isolatedDB(t)
	if err := Migrate(ctx, db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	original := batchRegistry(600)
	if err := Seed(ctx, db, original); err != nil {
		t.Fatal(err)
	}
	for _, change := range []struct{ name, sql string }{
		{"type", `UPDATE entities SET entity_type='vehicle' WHERE entity_id='entity-000599'`},
		{"catalog", `UPDATE entities SET task_catalog='["delete"]' WHERE entity_id='entity-000599'`},
		{"source", `UPDATE entities SET owner_source_id='other' WHERE entity_id='entity-000599'`},
		{"generation", `UPDATE entities SET source_generation=2 WHERE entity_id='entity-000599'`},
		{"executor", `UPDATE entities SET executor_id='other' WHERE entity_id='entity-000599'`},
	} {
		t.Run(change.name, func(t *testing.T) {
			if _, err := db.Exec(change.sql); err != nil {
				t.Fatal(err)
			}
			if err := CheckBindings(ctx, db, original); err == nil {
				t.Fatal("startup accepted changed entity")
			}
			if err := SeedWithEntityExpansion(ctx, db, batchRegistry(1200)); err == nil {
				t.Fatal("expansion accepted changed entity")
			}
			var count int
			if err := db.QueryRow(`SELECT COUNT(*) FROM course_bindings`).Scan(&count); err != nil || count != 601 {
				t.Fatalf("partial expansion persisted: %d %v", count, err)
			}
			if _, err := db.Exec(`UPDATE entities SET entity_type='drone',task_catalog='["inspect","move"]',owner_source_id='source',source_generation=1,executor_id='executor' WHERE entity_id='entity-000599'`); err != nil {
				t.Fatal(err)
			}
			if err := CheckBindings(ctx, db, original); err != nil {
				t.Fatal("rollback changed original bindings", err)
			}
		})
	}
	for _, mutation := range []string{"tenant", "credential"} {
		t.Run(mutation, func(t *testing.T) {
			query := `UPDATE integration_sources SET tenant_id='other' WHERE source_id='source'`
			if mutation == "credential" {
				query = `UPDATE integration_sources SET credentials_hash='wrong' WHERE source_id='source'`
			}
			if _, err := db.Exec(query); err != nil {
				t.Fatal(err)
			}
			if err := SeedWithEntityExpansion(ctx, db, batchRegistry(1200)); err == nil {
				t.Fatal("source conflict accepted")
			}
			if _, err := db.Exec(`UPDATE integration_sources SET tenant_id='batch',credentials_hash=? WHERE source_id='source'`, digest([]byte("batch-only-machine-credential"))); err != nil {
				t.Fatal(err)
			}
			if err := CheckBindings(ctx, db, original); err != nil {
				t.Fatal("source conflict did not roll back", err)
			}
		})
	}
	if err := CheckBindings(ctx, db, batchRegistry(1200)); err == nil {
		t.Fatal("startup accepted missing bindings")
	}
	if err := SeedWithEntityExpansion(ctx, db, batchRegistry(1200)); err != nil {
		t.Fatal(err)
	}
	if err := CheckBindings(ctx, db, batchRegistry(1200)); err != nil {
		t.Fatal(err)
	}
	if err := SeedWithEntityExpansion(ctx, db, original); err == nil {
		t.Fatal("expansion removed existing bindings")
	}
	// JSON 清单存在不能掩盖实际实体缺失，启动检查必须继续读取实体表。
	if _, err := db.Exec(`DELETE FROM entities WHERE tenant_id='batch' AND entity_id='entity-001199'`); err != nil {
		t.Fatal(err)
	}
	if err := CheckBindings(ctx, db, batchRegistry(1200)); err == nil {
		t.Fatal("startup accepted a missing persisted entity")
	}
}
