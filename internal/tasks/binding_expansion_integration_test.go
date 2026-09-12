//go:build integration

package tasks

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"example.com/meshops-course/internal/platform"
)

// Exercise the exact persisted six-to-thirty manifest upgrade in an isolated
// database. The old task/outbox facts and machine credential hashes survive.
func TestDemoEntityExpansionIsAdditiveAndAtomic(t *testing.T) {
	ctx := context.Background()
	db := isolatedDB(t)
	for _, kind := range []string{"PERSON", "DRONE", "VEHICLE", "ROBOT", "SENSOR", "FACILITY"} {
		t.Setenv("MESHOPS_"+kind+"_SOURCE_TOKEN", strings.Repeat("s", 32)+kind)
		t.Setenv("MESHOPS_"+kind+"_EXECUTOR_TOKEN", strings.Repeat("e", 32)+kind)
	}
	for _, role := range []string{"OPERATOR", "ADMIN", "TASK", "DISPATCHER"} {
		t.Setenv("MESHOPS_"+role+"_TOKEN", strings.Repeat("a", 32)+role)
	}
	load := func() *platform.Registry {
		t.Helper()
		r, err := platform.LoadRegistry("../../configs/simulation.yaml")
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	old := load()
	for key, source := range old.Sources {
		for rawID := range source.Entities {
			if !strings.HasSuffix(rawID, "-001") {
				delete(source.Entities, rawID)
			}
		}
		old.Sources[key] = source
	}
	for key, binding := range old.Bindings {
		if !strings.HasSuffix(binding.EntityID, "-001") {
			delete(old.Bindings, key)
		}
	}
	execSQLFile(t, db, "../../migrations/001_initial_schema.sql")
	execSQLFile(t, db, "../../testdata/migrations/001_existing_task.sql")
	if err := Migrate(ctx, db, "../../migrations"); err != nil {
		t.Fatal(err)
	}
	if err := Seed(ctx, db, old); err != nil {
		t.Fatal(err)
	}
	full := load()
	if err := Seed(ctx, db, full); err == nil {
		t.Fatal("ordinary seed allowed expansion")
	}
	checkCount := func(want int) {
		t.Helper()
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM entities WHERE tenant_id='demo_tenant'`).Scan(&count); err != nil || count != want {
			t.Fatal("entity count", count, want, err)
		}
	}
	checkCount(6)
	// A conflicting credential must roll back source JSON expansion and inserts.
	corrupt := load()
	corrupt.Credentials["demo_tenant:personnel_sim"] = strings.Repeat("z", 40)
	if err := SeedWithEntityExpansion(ctx, db, corrupt); err == nil {
		t.Fatal("credential replacement accepted")
	}
	checkCount(6)
	if err := CheckBindings(ctx, db, old); err != nil {
		t.Fatal("failed expansion changed old bindings", err)
	}
	for n := 0; n < 2; n++ {
		if err := SeedWithEntityExpansion(ctx, db, full); err != nil {
			t.Fatal("expansion/repeat", n, err)
		}
	}
	checkCount(30)
	if err := CheckBindings(ctx, db, full); err != nil {
		t.Fatal(err)
	}
	if err := SeedWithEntityExpansion(ctx, db, old); err == nil {
		t.Fatal("binding removal accepted")
	}
	reassigned := load()
	binding := reassigned.Bindings["demo_tenant:drone-001"]
	binding.ExecutorID = "other_executor"
	reassigned.Bindings["demo_tenant:drone-001"] = binding
	if err := SeedWithEntityExpansion(ctx, db, reassigned); err == nil {
		t.Fatal("executor reassignment accepted")
	}
	if err := CheckBindings(ctx, db, full); err != nil {
		t.Fatal("rejected change damaged bindings", err)
	}
	var executionKey string
	if err := db.QueryRow(`SELECT execution_key FROM tasks WHERE task_id='legacy-task'`).Scan(&executionKey); err != nil || executionKey != "legacy-task" {
		t.Fatal("task identity changed", executionKey, err)
	}
	var pending int
	if err := db.QueryRow(`SELECT COUNT(*) FROM outbox_events WHERE aggregate_id='legacy-task' AND published_at IS NULL`).Scan(&pending); err != nil || pending != 1 {
		t.Fatal("pending task event changed", pending, err)
	}
	for _, source := range full.Sources {
		var hash string
		if err := db.QueryRow(`SELECT credentials_hash FROM integration_sources WHERE source_id=?`, source.ID).Scan(&hash); err != nil {
			t.Fatal(err)
		}
		token, err := full.Credential(source.TenantID, source.ID)
		if err != nil || hash != digest([]byte(token)) {
			t.Fatal(fmt.Sprintf("credential changed for %s", source.ID), err)
		}
	}
}
