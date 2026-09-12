package app

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"
)

// Readiness is false until the RPC registration callback has run. Each probe
// shares one deadline so several unavailable dependencies cannot extend it.
func readiness(ready *atomic.Bool, checks ...func(context.Context) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !ready.Load() {
			http.Error(w, "starting or stopping", http.StatusServiceUnavailable)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		for _, check := range checks {
			if err := check(ctx); err != nil {
				http.Error(w, "dependency unavailable", http.StatusServiceUnavailable)
				return
			}
		}
		w.Write([]byte("ready\n"))
	}
}
