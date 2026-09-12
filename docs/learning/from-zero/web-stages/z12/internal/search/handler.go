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

// Handle 仅在所有行均已应用到带版本的投影后返回 nil。
// 适用于仅在处理函数成功后提交位点的消费者。消费者还必须
// 单独拒绝因数据保留期限产生的缺口，不能跳过缺失数据。
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
