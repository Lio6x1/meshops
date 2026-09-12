package bus

import (
	"context"
	"fmt"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/segmentio/kafka-go"
	"log/slog"
)

var retentionGaps = promauto.NewCounter(prometheus.CounterOpts{Name: "meshops_kafka_retention_gap_detections_total", Help: "Observed consumer retention gaps; rebalance can detect the same uncommitted gap again. Details are in structured logs; no entity or group labels."})

// RetentionGap means a committed next offset no longer exists in the retained
// log. Resuming at First is degraded recovery, not proof that no data was lost.
type RetentionGap struct {
	Group, Topic    string
	Partition       int
	Expected, First int64
}

func (g *RetentionGap) Error() string {
	return fmt.Sprintf("Kafka retention gap group=%s topic=%s partition=%d expected=%d retained_first=%d", g.Group, g.Topic, g.Partition, g.Expected, g.First)
}

func (k *Kafka) recordGap(ctx context.Context, group, topic string, partition int, expected, first int64) {
	k.gapDetections.Add(1)
	retentionGaps.Inc()
	slog.ErrorContext(ctx, "Kafka retention gap detected", "group", group, "topic", topic, "partition", partition, "expected_offset", expected, "retained_first", first, "missing_offsets", first-expected)
}

func (k *Kafka) firstOffset(ctx context.Context, topic string, partition int) (int64, error) {
	c, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	bounds, err := k.client().ListOffsets(c, &kafka.ListOffsetsRequest{Topics: map[string][]kafka.OffsetRequest{topic: {kafka.FirstOffsetOf(partition)}}})
	if err != nil {
		return 0, err
	}
	for _, p := range bounds.Topics[topic] {
		if p.Partition == partition {
			if p.Error != nil {
				return 0, p.Error
			}
			if p.FirstOffset >= 0 {
				return p.FirstOffset, nil
			}
		}
	}
	return 0, fmt.Errorf("missing retained beginning for %s/%d", topic, partition)
}
