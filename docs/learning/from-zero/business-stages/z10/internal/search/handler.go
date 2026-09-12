package search

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

type TaskIndex interface {
	Put(context.Context, Document) (ApplyResult, error)
}

type Handler struct {
	Database string
	Index    TaskIndex
}

// Handle returns nil only after every row has reached the versioned projection.
// It is suitable for a consumer that commits only after handler success. The
// consumer must separately reject retention gaps rather than skip missing data.
func (h *Handler) Handle(ctx context.Context, data []byte) error {
	if h.Index == nil {
		return fmt.Errorf("task index required")
	}
	docs, err := DecodeCanal(data, h.Database)
	if err != nil {
		failedRecords.Inc()
		slog.WarnContext(ctx, "search CDC validation failed", "error", err)
		return err
	}
	for _, doc := range docs {
		if err := ctx.Err(); err != nil {
			return err
		}
		result, e := h.Index.Put(ctx, doc)
		if e != nil {
			failedRecords.Inc()
			indexAttempts.WithLabelValues("failed").Inc()
			slog.WarnContext(ctx, "search indexing failed", "error", e)
			return e
		}
		switch result {
		case Applied, Duplicate, Stale:
			indexAttempts.WithLabelValues(string(result)).Inc()
		}
	}
	appliedRecords.Inc()
	lastSuccess.Set(float64(time.Now().Unix()))
	return nil
}
