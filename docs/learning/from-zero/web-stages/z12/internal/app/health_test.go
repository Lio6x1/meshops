package app

import (
	"context"
	"errors"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestReadinessRequiresRPCRegistrationAndDependencies(t *testing.T) {
	var ready atomic.Bool
	calls := 0
	dependency := func(ctx context.Context) error {
		calls++
		if _, ok := ctx.Deadline(); !ok {
			t.Error("dependency probe has no deadline")
		}
		return nil
	}
	handler := readiness(&ready, dependency)
	check := func(want int) {
		t.Helper()
		w := httptest.NewRecorder()
		handler(w, httptest.NewRequest("GET", "/readyz", nil))
		if w.Code != want {
			t.Fatalf("status=%d want=%d", w.Code, want)
		}
	}
	check(503)
	if calls != 0 {
		t.Fatal("unregistered server probed dependencies")
	}
	ready.Store(true)
	check(200)
	handler = readiness(&ready, func(context.Context) error { return errors.New("dependency down") })
	check(503)
	ready.Store(false)
	handler = readiness(&ready, dependency)
	check(503)
}
