package app

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"
)

// RPC 注册回调执行前，就绪状态始终为 false。每次探测
// 共用一个截止时间，避免多个不可用依赖使探测超时延长。
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
