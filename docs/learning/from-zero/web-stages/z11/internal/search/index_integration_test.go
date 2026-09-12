//go:build integration

package search

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRealElasticsearchVersionProjection(t *testing.T) {
	endpoint := os.Getenv("MESHOPS_TEST_ES_ENDPOINT")
	if endpoint == "" {
		t.Skip("MESHOPS_TEST_ES_ENDPOINT required")
	}
	es, err := NewIndex(endpoint, "meshops-test-"+strconv.FormatInt(time.Now().UnixNano(), 10), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = es.Create(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		c, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		code, _, e := es.request(c, http.MethodDelete, "/"+es.name, nil)
		if e != nil || code != 200 {
			t.Errorf("isolated index cleanup: %d %v", code, e)
		}
	})
	docs, err := DecodeCanal(canalMessage(taskRow()), "meshops_course")
	if err != nil {
		t.Fatal(err)
	}
	d := docs[0]
	if result, e := es.Put(ctx, d); e != nil || result != Applied {
		t.Fatalf("initial %s %v", result, e)
	}
	if result, e := es.Put(ctx, d); e != nil || result != Duplicate {
		t.Fatalf("duplicate %s %v", result, e)
	}
	d.StatusVersion++
	d.Status = "ACKED"
	if result, e := es.Put(ctx, d); e != nil || result != Applied {
		t.Fatalf("update %s %v", result, e)
	}
	if result, e := es.Put(ctx, docs[0]); e != nil || result != Stale {
		t.Fatalf("stale %s %v", result, e)
	}
	d.Note = "conflicting same version"
	if _, e := es.Put(ctx, d); e == nil {
		t.Fatal("same version corruption accepted")
	}
	second := docs[0]
	second.TaskID = "task-2"
	if _, err = es.Put(ctx, second); err != nil {
		t.Fatal(err)
	}
	foreign := second
	foreign.TenantID = "tenant-b"
	if _, err = es.Put(ctx, foreign); err != nil {
		t.Fatal(err)
	}
	if code, _, e := es.request(ctx, http.MethodPost, "/"+es.name+"/_refresh", nil); e != nil || code != 200 {
		t.Fatal("refresh failed", code, e)
	}
	key := []byte(strings.Repeat("k", 32))
	filter := Filter{Keyword: "屋顶", PageSize: 1}
	first, err := es.Search(ctx, "tenant-a", filter, "", key)
	if err != nil || len(first.Tasks) != 1 || first.Tasks[0].TaskID != "task-1" || first.NextCursor == "" {
		t.Fatalf("first search: %+v %v", first, err)
	}
	late := second
	late.TaskID = "task-3"
	if _, err = es.Put(ctx, late); err != nil {
		t.Fatal(err)
	}
	if code, _, e := es.request(ctx, http.MethodPost, "/"+es.name+"/_refresh", nil); e != nil || code != 200 {
		t.Fatal("refresh failed", code, e)
	}
	last, err := es.Search(ctx, "tenant-a", filter, first.NextCursor, key)
	if err != nil || len(last.Tasks) != 1 || last.Tasks[0].TaskID != "task-2" || last.NextCursor != "" {
		t.Fatalf("PIT continuation: %+v %v", last, err)
	}
	filter.PageSize = 10
	fresh, err := es.Search(ctx, "tenant-a", filter, "", key)
	if err != nil || len(fresh.Tasks) != 3 {
		t.Fatalf("fresh search: %+v %v", fresh, err)
	}
	filtered, err := es.Search(ctx, "tenant-a", Filter{Status: "ACKED", TargetEntityID: "drone-001", CreatedFrom: docs[0].CreatedAt}, "", key)
	if err != nil || len(filtered.Tasks) != 1 || filtered.Tasks[0].TaskID != "task-1" {
		t.Fatalf("combined filters: %+v %v", filtered, err)
	}
	excluded, err := es.Search(ctx, "tenant-a", Filter{CreatedBefore: docs[0].CreatedAt}, "", key)
	if err != nil || len(excluded.Tasks) != 0 {
		t.Fatalf("exclusive upper timestamp: %+v %v", excluded, err)
	}
	uuid, err := es.UUID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bound := es.Bound(uuid)
	if err = bound.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if code, _, e := es.request(ctx, http.MethodDelete, "/"+es.name, nil); e != nil || code != 200 {
		t.Fatal("test index replacement failed", code, e)
	}
	if err = es.Create(ctx); err != nil {
		t.Fatal(err)
	}
	if err = bound.Check(ctx); err == nil {
		t.Fatal("same-name empty index accepted as original bootstrap")
	}
}
