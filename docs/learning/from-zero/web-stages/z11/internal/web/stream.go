package web

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	entityv1 "example.com/meshops-course/gen/entity/v1"
	"example.com/meshops-course/internal/platform"
	"google.golang.org/protobuf/encoding/protojson"
)

// stream 保留上游的流量控制边界，不在此引入无界的扇出队列。
// 慢浏览器受写入截止时间约束，取消信号会继续传递到 gRPC。
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	p := r.Context().Value(sessionKey{}).(*session)
	ids := strings.Split(r.URL.Query().Get("entity_ids"), ",")
	if len(ids) < 1 || len(ids) > 100 {
		webError(w, 400, "subscribe to 1..100 entities")
		return
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !s.allowedEntity(p, id) || seen[id] {
			webError(w, 403, "unknown, duplicate or unauthorized entity")
			return
		}
		seen[id] = true
	}
	if p.streams.Add(1) > 2 {
		p.streams.Add(-1)
		webError(w, 429, "session subscription limit reached")
		return
	}
	defer p.streams.Add(-1)
	if s.streams.Add(1) > 32 {
		s.streams.Add(-1)
		webError(w, 429, "subscription capacity reached")
		return
	}
	defer s.streams.Add(-1)
	ctx, cancel := context.WithTimeout(r.Context(), p.expires.Sub(s.cfg.Now()))
	defer cancel()
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		select {
		case <-p.done:
			cancel()
		case <-ctx.Done():
		}
	}()
	defer func() { cancel(); <-joined }()
	source, e := s.cfg.Entity.Subscribe(platform.Outgoing(ctx, p.token), &entityv1.SubscribeRequest{EntityIds: ids})
	if e != nil {
		webError(w, 503, "entity subscription unavailable")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	control := http.NewResponseController(w)
	if e = control.Flush(); e != nil {
		return
	}
	for {
		update, err := source.Recv()
		if err != nil {
			if ctx.Err() == nil {
				_ = control.SetWriteDeadline(time.Now().Add(5 * time.Second))
				_, _ = fmt.Fprint(w, "event: error\ndata: {\"code\":14,\"message\":\"subscription interrupted; resynchronize\"}\n\n")
				_ = control.Flush()
			}
			return
		}
		data, err := protojson.Marshal(update)
		if err != nil {
			return
		}
		if err = control.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
			return
		}
		if _, err = fmt.Fprintf(w, "event: update\ndata: %s\n\n", data); err != nil {
			return
		}
		if err = control.Flush(); err != nil {
			return
		}
	}
}
