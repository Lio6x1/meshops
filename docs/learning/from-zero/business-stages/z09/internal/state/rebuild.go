package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	commonv1 "example.com/meshops-course/gen/common/v1"
	"example.com/meshops-course/internal/platform"
	"fmt"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
	"io"
	"log/slog"
	"os"
	"regexp"
	"sort"
	"strconv"
	"time"
)

type ExpectedState struct {
	TenantID         string `json:"tenant_id"`
	EntityID         string `json:"entity_id"`
	SourceGeneration int64  `json:"source_generation"`
	EntityVersion    int64  `json:"entity_version"`
	Operation        string `json:"operation"`
}
type PartitionBounds struct {
	Partition int   `json:"partition"`
	Start     int64 `json:"start"`
	End       int64 `json:"end"`
}

var generationPattern = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)
var switchView = redis.NewScript(`if redis.call('GET',KEYS[1])~=ARGV[1] then return 0 end; redis.call('SET',KEYS[1],ARGV[2]); return 1`)

func (e *Entity) loadExpected(path string) (map[string]ExpectedState, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(raw) > 16<<20 {
		return nil, fmt.Errorf("manifest exceeds 16 MiB")
	}
	var entries []ExpectedState
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&entries); err != nil {
		return nil, err
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("trailing manifest JSON")
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("nonempty independently generated manifest required")
	}
	expected := map[string]ExpectedState{}
	for _, v := range entries {
		b, ok := e.registry.Lookup(v.TenantID, v.EntityID)
		if !validID(v.TenantID, 64) || !validID(v.EntityID, 128) || !ok || v.SourceGeneration != b.SourceGeneration || v.EntityVersion < 1 || v.EntityVersion > MaxVersion || !oneOf(v.Operation, "UPSERT", "DELETE", "ENTITY_OPERATION_UPSERT", "ENTITY_OPERATION_DELETE") {
			return nil, fmt.Errorf("invalid manifest entry for %s", platform.Key(v.TenantID, v.EntityID))
		}
		key := platform.Key(v.TenantID, v.EntityID)
		if _, ok = expected[key]; ok {
			return nil, fmt.Errorf("duplicate manifest entry %s", key)
		}
		expected[key] = v
	}
	return expected, nil
}

// VerifyGeneration compares the shadow namespace with an independent fixture
// manifest. It never derives expected versions from the old Redis namespace.
func (e *Entity) VerifyGeneration(ctx context.Context, generation string, expected map[string]ExpectedState) error {
	var missing []string
	for key, want := range expected {
		got, err := e.read(ctx, generation, want.TenantID, want.EntityID)
		if err != nil {
			return err
		}
		deleted := oneOf(want.Operation, "DELETE", "ENTITY_OPERATION_DELETE")
		if got.version != want.EntityVersion || got.generation != want.SourceGeneration || got.deleted != deleted {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("incomplete rebuild; missing or mismatched latest facts: %v", missing)
	}
	return nil
}

// Rebuild is a maintenance operation. Pause generation, drain gateway queues,
// and stop the ordinary Entity process BEFORE invoking it. It uses independent
// partition readers and never commits or resets the ordinary projector group.
func (e *Entity) Rebuild(ctx context.Context, generation, manifestPath string) error {
	if !generationPattern.MatchString(generation) {
		return fmt.Errorf("generation must be a lower-case UUID")
	}
	expected, err := e.loadExpected(manifestPath)
	if err != nil {
		return err
	}
	old, err := e.generation(ctx)
	if err != nil {
		return err
	}
	if old == generation {
		return fmt.Errorf("shadow generation must differ from active")
	}
	if len(e.cfg.KafkaBrokers) == 0 {
		return fmt.Errorf("kafka brokers required")
	}
	created, err := e.redis.SetNX(ctx, "meshops:view:build:"+generation, "building", 0).Result()
	if err != nil {
		return err
	}
	if !created {
		return fmt.Errorf("generation already used; choose a new UUID")
	}
	iterator := e.redis.Scan(ctx, 0, "view:"+generation+":tenant:*:entity:*:snapshot", 1).Iterator()
	if iterator.Next(ctx) {
		return fmt.Errorf("shadow namespace must be empty")
	}
	if err = iterator.Err(); err != nil {
		return err
	}
	topic := e.cfg.TopicPrefix + "entity-state-events.v1"
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	conn, err := kafka.DialContext(dialCtx, "tcp", e.cfg.KafkaBrokers[0])
	cancel()
	if err != nil {
		return err
	}
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	parts, err := conn.ReadPartitions(topic)
	conn.Close()
	if err != nil {
		return err
	}
	if len(parts) == 0 {
		return fmt.Errorf("topic has no partitions")
	}
	sort.Slice(parts, func(i, j int) bool { return parts[i].ID < parts[j].ID })
	bounds := make([]PartitionBounds, 0, len(parts))
	// Capture every partition boundary before replay begins. End is exclusive.
	for _, part := range parts {
		bound, err := rebuildPartitionBounds(ctx, e.cfg.KafkaBrokers[0], topic, part.ID)
		if err != nil {
			return err
		}
		bounds = append(bounds, bound)
	}
	encoded, err := json.Marshal(bounds)
	if err != nil {
		return err
	}
	if err = e.redis.Set(ctx, "meshops:view:bounds:"+generation, encoded, 0).Err(); err != nil {
		return err
	}
	observed := map[string]bool{}
	for _, bound := range bounds {
		if bound.Start >= bound.End {
			continue
		}
		reader := kafka.NewReader(kafka.ReaderConfig{Brokers: e.cfg.KafkaBrokers, Topic: topic, Partition: bound.Partition, MinBytes: 1, MaxBytes: 4 << 20, QueueCapacity: 1, MaxWait: time.Second})
		if err = reader.SetOffset(bound.Start); err != nil {
			reader.Close()
			return err
		}
		next := bound.Start
		for next < bound.End {
			readCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			message, readErr := reader.FetchMessage(readCtx)
			cancel()
			if readErr != nil {
				reader.Close()
				return fmt.Errorf("rebuild partition %d before exclusive end %d: %w", bound.Partition, bound.End, readErr)
			}
			if message.Offset >= bound.End {
				reader.Close()
				return fmt.Errorf("retained record gap before captured end in partition %d", bound.Partition)
			}
			if message.Offset != next {
				slog.WarnContext(ctx, "rebuild retained offset gap", "topic", topic, "partition", bound.Partition, "expected", next, "actual", message.Offset)
			}
			next = message.Offset + 1
			event, decodeErr := e.decode(message.Value)
			if decodeErr != nil {
				slog.WarnContext(ctx, "rebuild quarantine", "topic", topic, "partition", bound.Partition, "offset", message.Offset, "reason", "invalid_event")
				continue
			}
			observed[platform.Key(event.TenantId, event.EntityId)] = true
			result, projectErr := e.projectInto(ctx, generation, event, false)
			if projectErr != nil {
				reader.Close()
				return projectErr
			}
			if result == -2 {
				reader.Close()
				return fmt.Errorf("conflicting same-version input at partition %d offset %d", bound.Partition, message.Offset)
			}
		}
		if err = reader.Close(); err != nil {
			return err
		}
		slog.InfoContext(ctx, "rebuild partition complete", "partition", bound.Partition, "start", bound.Start, "end", bound.End)
	}
	for key := range observed {
		if _, ok := expected[key]; !ok {
			return fmt.Errorf("manifest omits replayed entity %s", key)
		}
	}
	if err = e.VerifyGeneration(ctx, generation, expected); err != nil {
		return err
	}
	switched, err := switchView.Run(ctx, e.redis, []string{activeKey}, old, generation).Int()
	if err != nil {
		return err
	}
	if switched != 1 {
		return errors.New("active generation changed during rebuild; shadow not activated")
	}
	if err = e.redis.Set(ctx, "meshops:view:build:"+generation, "verified", 0).Err(); err != nil {
		slog.WarnContext(ctx, "view activated but build marker unavailable", "generation", generation)
	}
	slog.InfoContext(ctx, "verified view activated", "generation", generation, "entities", strconv.Itoa(len(expected)))
	return nil
}

func rebuildPartitionBounds(ctx context.Context, broker, topic string, partition int) (PartitionBounds, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	deadline, _ := ctx.Deadline()
	dialer := &kafka.Dialer{Timeout: 10 * time.Second}
	for {
		// Metadata can name a leader before it serves offsets, or become stale
		// during election. Reconnect through fresh metadata on each attempt.
		conn, err := dialer.DialLeader(ctx, "tcp", broker, topic, partition)
		if err == nil {
			_ = conn.SetDeadline(deadline)
			stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
			first, last, readErr := conn.ReadOffsets()
			stop()
			conn.Close()
			err = readErr
			if err == nil {
				return PartitionBounds{Partition: partition, Start: first, End: last}, nil
			}
		}
		if ctx.Err() != nil {
			return PartitionBounds{}, fmt.Errorf("rebuild bounds %s/%d: %w", topic, partition, ctx.Err())
		}
		if !errors.Is(err, kafka.NotLeaderForPartition) && !errors.Is(err, kafka.LeaderNotAvailable) {
			return PartitionBounds{}, fmt.Errorf("rebuild bounds %s/%d: %w", topic, partition, err)
		}
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return PartitionBounds{}, fmt.Errorf("rebuild bounds %s/%d: %w", topic, partition, ctx.Err())
		case <-timer.C:
		}
	}
}

// OperationName is shared by input-manifest generators in tests and tooling.
func OperationName(op commonv1.EntityOperation) string {
	if op == commonv1.EntityOperation_ENTITY_OPERATION_DELETE {
		return "DELETE"
	}
	return "UPSERT"
}
