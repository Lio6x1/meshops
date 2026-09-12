package search

import (
	"context"
	"errors"
	"fmt"

	"github.com/segmentio/kafka-go"
)

// CDC maintenance keeps the dedicated topic intact. With Canal stopped, its
// end offset separates obsolete CDC from the new snapshot's incremental input.
// The snapshot covers all earlier database changes. Never reset a live group.
type cdcMaintenance struct {
	k            *kafka.Client
	topic, group string
}

func CDCBoundary(ctx context.Context, k *kafka.Client) (int64, error) {
	return (cdcMaintenance{k, CDCTopic, CDCGroup}).boundary(ctx)
}
func CommitCDCStart(ctx context.Context, k *kafka.Client, start int64) error {
	return (cdcMaintenance{k, CDCTopic, CDCGroup}).commit(ctx, start)
}

func (p cdcMaintenance) idle(ctx context.Context) error {
	r, err := p.k.DescribeGroups(ctx, &kafka.DescribeGroupsRequest{GroupIDs: []string{p.group}})
	if err != nil {
		return fmt.Errorf("inspect search consumer group: %w", err)
	}
	if len(r.Groups) != 1 || r.Groups[0].GroupID != p.group {
		return fmt.Errorf("search group response missing")
	}
	g := r.Groups[0]
	if errors.Is(g.Error, kafka.GroupIdNotFound) {
		return nil
	}
	if g.Error != nil {
		return fmt.Errorf("inspect search group: %w", g.Error)
	}
	if len(g.Members) != 0 || (g.GroupState != "Empty" && g.GroupState != "Dead") {
		return fmt.Errorf("search group still active; stop Search and wait for membership expiry")
	}
	return nil
}

func (p cdcMaintenance) boundary(ctx context.Context) (int64, error) {
	if err := p.idle(ctx); err != nil {
		return 0, err
	}
	r, err := p.k.ListOffsets(ctx, &kafka.ListOffsetsRequest{Topics: map[string][]kafka.OffsetRequest{p.topic: {kafka.LastOffsetOf(0)}}})
	if err != nil {
		return 0, fmt.Errorf("read CDC boundary: %w", err)
	}
	parts := r.Topics[p.topic]
	if len(parts) != 1 || parts[0].Partition != 0 || parts[0].Error != nil || parts[0].LastOffset < 0 {
		return 0, fmt.Errorf("CDC boundary unavailable")
	}
	return parts[0].LastOffset, nil
}

func (p cdcMaintenance) commit(ctx context.Context, start int64) error {
	if start < 0 {
		return fmt.Errorf("invalid CDC start")
	}
	end, err := p.boundary(ctx)
	if err != nil {
		return err
	}
	if end != start {
		return fmt.Errorf("CDC changed during snapshot; Canal must remain stopped")
	}
	r, err := p.k.OffsetCommit(ctx, &kafka.OffsetCommitRequest{GroupID: p.group, GenerationID: -1, Topics: map[string][]kafka.OffsetCommit{p.topic: {{Partition: 0, Offset: start, Metadata: "full task snapshot"}}}})
	if err != nil {
		return fmt.Errorf("commit CDC recovery start: %w", err)
	}
	parts := r.Topics[p.topic]
	if len(parts) != 1 || parts[0].Partition != 0 || parts[0].Error != nil {
		return fmt.Errorf("CDC recovery start was not committed")
	}
	return nil
}
