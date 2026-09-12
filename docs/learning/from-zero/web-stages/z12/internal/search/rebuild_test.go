package search

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResetTaskIndexRefusesOtherResources(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/"+TaskIndexName {
			t.Errorf("unexpected target %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"acknowledged":true}`))
	}))
	defer srv.Close()
	other, _ := NewIndex(srv.URL, "other-business-index", nil)
	if err := other.ResetTaskIndex(context.Background()); err == nil || requests != 0 {
		t.Fatal("reset escaped the task index boundary")
	}
	es, _ := NewIndex(srv.URL, TaskIndexName, nil)
	if err := es.ResetTaskIndex(context.Background()); err != nil || requests != 2 {
		t.Fatalf("reset: %v, requests %d", err, requests)
	}
}

func TestResetTaskIndexHandlesMissingButNotFailedDelete(t *testing.T) {
	for _, status := range []int{404, 403, 503} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			created := false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodDelete {
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"error":{"type":"index_not_found_exception"}}`))
					return
				}
				created = true
				_, _ = w.Write([]byte(`{"acknowledged":true}`))
			}))
			defer srv.Close()
			es, _ := NewIndex(srv.URL, TaskIndexName, nil)
			err := es.ResetTaskIndex(context.Background())
			if status == 404 {
				if err != nil || !created {
					t.Fatalf("missing index recovery: %v", err)
				}
			} else if err == nil || created {
				t.Fatal("delete failure was hidden")
			}
		})
	}
}
