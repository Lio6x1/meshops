package state

import (
	"context"
	"fmt"
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
