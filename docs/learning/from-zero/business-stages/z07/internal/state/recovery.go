package state

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// ProjectorGroup 将 Kafka 进度绑定到其对应的 Redis 命名空间。
// 命名空间丢失后必须使用新消费组重放，不能复用已失去对应视图的进度。
func (e *Entity) ProjectorGroup(ctx context.Context) (string, error) {
	generation, err := e.generation(ctx)
	if err != nil {
		return "", err
	}
	return ProjectorGroupName(e.cfg.ConsumerGroupPrefix, generation), nil
}

// ProjectorGroupName 让观测工具能在 Redis 故障前保存消费组身份。
// Generation 必须是从 Entity 观测到的活动视图 ID。
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
