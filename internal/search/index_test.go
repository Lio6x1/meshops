package search

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIndexVersionConflict(t *testing.T) {
	docs, _ := DecodeCanal(canalMessage(taskRow()), "meshops_course")
	doc := docs[0]
	for _, scenario := range []struct {
		name    string
		version int64
		change  bool
		fail    bool
	}{
		{"duplicate", doc.Version(), false, false},
		{"old event", doc.Version() + 1, true, false},
		{"same version different content", doc.Version(), true, true},
		{"unexpected lower version", doc.Version() - 1, false, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			current := doc
			current.StatusVersion = scenario.version - 1
			if scenario.change {
				current.Note = "changed"
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPut {
					if r.URL.Query().Get("version_type") != "external" || r.URL.Query().Get("version") != "2" {
						t.Error("missing external version")
					}
					w.WriteHeader(409)
					_, _ = w.Write([]byte(`{"error":{"type":"version_conflict_engine_exception"}}`))
					return
				}
				if r.Method != http.MethodGet {
					t.Error("unexpected method")
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"found": true, "_version": scenario.version, "_source": current})
			}))
			defer server.Close()
			es, err := NewIndex(server.URL, "meshops-tasks", server.Client())
			if err != nil {
				t.Fatal(err)
			}
			_, err = es.Put(context.Background(), doc)
			if (err != nil) != scenario.fail {
				t.Fatalf("err=%v want fail=%v", err, scenario.fail)
			}
		})
	}
}

func TestIndexDoesNotHideDependencyOrNonVersionErrors(t *testing.T) {
	docs, _ := DecodeCanal(canalMessage(taskRow()), "meshops_course")
	for _, code := range []int{400, 401, 409, 429, 500, 503} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
			_, _ = w.Write([]byte(`{"error":{"type":"other_exception"}}`))
		}))
		es, _ := NewIndex(server.URL, "meshops-tasks", server.Client())
		if _, err := es.Put(context.Background(), docs[0]); err == nil {
			t.Errorf("status %d was hidden", code)
		}
		server.Close()
	}
}

func TestIndexEndpointAndIndexValidation(t *testing.T) {
	for _, endpoint := range []string{"http://0.0.0.0:9200", "http://example.com:9200", "http://127.0.0.1:9200/path", "http://user:pass@127.0.0.1:9200"} {
		if _, err := NewIndex(endpoint, "meshops-tasks", nil); err == nil {
			t.Fatalf("accepted %s", endpoint)
		}
	}
	for _, name := range []string{"", "Tasks", "tasks/_delete_by_query", "*", "_all"} {
		if _, err := NewIndex("http://127.0.0.1:9200", name, nil); err == nil {
			t.Fatalf("accepted index %s", name)
		}
	}
}
