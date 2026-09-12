package search

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSearchTenantPagingAndCursorBinding(t *testing.T) {
	docs, _ := DecodeCanal(canalMessage(taskRow()), "meshops_course")
	var searches, closed int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/meshops-tasks/_pit" {
			_, _ = w.Write([]byte(`{"id":"pit-1"}`))
			return
		}
		if r.Method == http.MethodDelete {
			closed++
			_, _ = w.Write([]byte(`{"succeeded":true}`))
			return
		}
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		raw, _ := json.Marshal(request["query"])
		if !strings.Contains(string(raw), `"tenant_id":"tenant-a"`) {
			t.Error("tenant filter missing")
		}
		if searches > 0 && request["search_after"] == nil {
			t.Error("search_after missing")
		}
		searches++
		hits := []any{}
		if searches == 1 {
			for i := 0; i < 2; i++ {
				d := docs[0]
				d.TaskID = string(rune('a' + i))
				hits = append(hits, map[string]any{"_source": d, "sort": []any{d.CreatedAt, d.TaskID, i}})
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"pit_id": "pit-2", "hits": map[string]any{"hits": hits}})
	}))
	defer server.Close()
	es, _ := NewIndex(server.URL, "meshops-tasks", server.Client())
	key := []byte(strings.Repeat("k", 32))
	filter := Filter{Keyword: "屋顶", PageSize: 1}
	page, err := es.Search(context.Background(), "tenant-a", filter, "", key)
	if err != nil || len(page.Tasks) != 1 || page.NextCursor == "" {
		t.Fatalf("first page: %+v %v", page, err)
	}
	for _, change := range []struct {
		tenant, token string
		filter        Filter
	}{
		{"tenant-b", page.NextCursor, filter},
		{"tenant-a", page.NextCursor + "x", filter},
		{"tenant-a", page.NextCursor, Filter{Keyword: "different", PageSize: 1}},
	} {
		if _, e := es.Search(context.Background(), change.tenant, change.filter, change.token, key); e == nil {
			t.Fatal("accepted foreign/changed cursor")
		}
	}
	if searches != 1 {
		t.Fatal("invalid cursor reached ES")
	}
	last, err := es.Search(context.Background(), "tenant-a", filter, page.NextCursor, key)
	if err != nil || last.NextCursor != "" || closed != 1 {
		t.Fatalf("last page %+v err=%v closed=%d", last, err, closed)
	}
}

func TestExpiredSearchCursor(t *testing.T) {
	key := []byte(strings.Repeat("k", 32))
	_, filter, _ := normalizeFilter(Filter{})
	token, err := encodeSearchCursor(searchCursor{Version: 1, Tenant: "a", Filter: filter, PIT: "pit", After: []json.RawMessage{json.RawMessage(`"date"`), json.RawMessage(`"id"`), json.RawMessage(`1`)}, Expires: time.Now().Add(-time.Second).Unix()}, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = decodeSearchCursor(token, key, "a", filter); err != ErrSearchExpired {
		t.Fatalf("expired cursor: %v", err)
	}
}

func TestSearchRejectsInvalidFiltersBeforeNetwork(t *testing.T) {
	es, _ := NewIndex("http://127.0.0.1:1", "meshops-tasks", nil)
	key := []byte(strings.Repeat("k", 32))
	for _, f := range []Filter{{PageSize: 101}, {PageSize: -1}, {Status: "UNKNOWN"}, {Keyword: strings.Repeat("a", 257)}, {CreatedFrom: "yesterday"}, {CreatedFrom: "2026-09-12T00:00:00Z", CreatedBefore: "2026-09-11T00:00:00Z"}} {
		if _, err := es.Search(context.Background(), "tenant-a", f, "", key); err == nil || !strings.Contains(err.Error(), "invalid search") {
			t.Fatalf("filter error: %v", err)
		}
	}
}
