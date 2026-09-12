package state

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/redis/go-redis/v9"
	"log/slog"
	"time"
)

// ProjectorGroup binds Kafka progress to the Redis namespace it describes. A
// lost namespace must replay from a fresh group instead of reusing lost progress.
func (e *Entity) ProjectorGroup(ctx context.Context) (string, error) {
	generation, err := e.generation(ctx)
	if err != nil {
		return "", err
	}
	return ProjectorGroupName(e.cfg.ConsumerGroupPrefix, generation), nil
}

// ProjectorGroupName lets observation tooling retain the group identity before
// a Redis outage. Generation must be the active view ID observed from Entity.
func ProjectorGroupName(prefix, generation string) string {
	return prefix + "entity-projector-v1-" + generation
}

// Bounds alone are not authority: a failed rebuild also writes them. Activation
// publishes the verified marker atomically with the namespace pointer.
var activateVerifiedView = redis.NewScript(`
if redis.call('GET',KEYS[1])~=ARGV[1] then return 0 end
if redis.call('GET',KEYS[2])~='building' or redis.call('EXISTS',KEYS[3])~=1 or redis.call('EXISTS',KEYS[4])~=1 then return -1 end
redis.call('SET',KEYS[2],'verified')
redis.call('SET',KEYS[1],ARGV[2])
return 1
`)

func (e *Entity) replayFloors(ctx context.Context, generation string) (map[int]int64, error) {
	values, err := e.redis.MGet(ctx, "meshops:view:build:"+generation, "meshops:view:bounds:"+generation, "meshops:view:topic:"+generation).Result()
	if err != nil {
		return nil, fmt.Errorf("read view recovery evidence: %w", err)
	}
	if values[0] == nil && values[1] == nil && values[2] == nil {
		return nil, nil
	}
	marker, ok := values[0].(string)
	raw, boundsOK := values[1].(string)
	topic, topicOK := values[2].(string)
	if !ok || marker != "verified" || !boundsOK || !topicOK || topic != e.cfg.TopicPrefix+"entity-state-events.v1" {
		return nil, fmt.Errorf("view recovery evidence incomplete; run a verified maintenance rebuild")
	}
	var bounds []PartitionBounds
	if err = json.Unmarshal([]byte(raw), &bounds); err != nil || len(bounds) != 3 {
		return nil, fmt.Errorf("invalid verified partition bounds; maintenance rebuild required")
	}
	floors := make(map[int]int64, len(bounds))
	for _, bound := range bounds {
		if _, exists := floors[bound.Partition]; exists || bound.Partition < 0 || bound.Partition >= 3 || bound.Start < 0 || bound.End < bound.Start {
			return nil, fmt.Errorf("invalid verified partition bounds; maintenance rebuild required")
		}
		floors[bound.Partition] = bound.End
	}
	return floors, nil
}

func (e *Entity) watchGeneration(ctx context.Context, generation string) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
		current, err := e.generation(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			slog.WarnContext(ctx, "view generation observation retry", "reason", "redis_unavailable")
			continue
		}
		if current != generation {
			return fmt.Errorf("active view changed while projector running; restart after maintenance")
		}
	}
}
