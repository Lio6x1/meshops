package state

import (
	"context"
	"example.com/meshops-course/internal/bus"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"log/slog"
)

var projectionResults = promauto.NewCounterVec(prometheus.CounterOpts{Name: "meshops_state_projection_total", Help: "State projection decisions; labels never contain entity IDs."}, []string{"result"})
var quarantineRecords = promauto.NewCounterVec(prometheus.CounterOpts{Name: "meshops_state_quarantine_total", Help: "Rejected state records by bounded reason."}, []string{"reason"})
var historyCandidates = promauto.NewCounterVec(prometheus.CounterOpts{Name: "meshops_history_candidates_total", Help: "Sample candidate outcomes by bounded reason."}, []string{"reason"})

func quarantine(ctx context.Context, reason string) {
	quarantineRecords.WithLabelValues(reason).Inc()
	record := bus.RecordMetadata(ctx)
	slog.WarnContext(ctx, "state quarantine", "reason", reason, "topic", record.Topic, "partition", record.Partition, "offset", record.Offset)
}
