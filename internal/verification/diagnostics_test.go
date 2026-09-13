package verification

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDiagnosticCaptureRetainsRawCountersWithoutEnvironment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("meshops_publish_errors_total 7\ngo_memstats_heap_alloc_bytes 512\n"))
	}))
	defer server.Close()
	env := &Environment{Processes: map[string]*Process{"ingest": {Metrics: strings.TrimPrefix(server.URL, "http://")}}, env: map[string]string{"TOKEN": "must-not-be-recorded"}}
	sample := captureDiagnostics(context.Background(), env)
	if !strings.Contains(sample.Metrics["ingest"], "meshops_publish_errors_total 7") || len(sample.Errors) != 0 {
		t.Fatal(sample)
	}
	if strings.Contains(sample.Metrics["ingest"], "must-not-be-recorded") {
		t.Fatal("credential leaked")
	}
}
