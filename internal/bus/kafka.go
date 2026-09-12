// Package bus makes the durable boundary explicit: publish waits for broker ACK,
// and consumption commits only after application work succeeds.
package bus

import (
	"context"
	"errors"
	"fmt"
	"github.com/segmentio/kafka-go"
	"log/slog"
	"sync/atomic"
	"time"
)

const operationTimeout = 5 * time.Second

type Kafka struct {
	brokers       []string
	writer        *kafka.Writer
	gapDetections atomic.Uint64
}
type metadataKey struct{}
type Record struct {
	Topic     string
	Partition int
	Offset    int64
}

func RecordMetadata(ctx context.Context) Record { v, _ := ctx.Value(metadataKey{}).(Record); return v }
func New(brokers []string) *Kafka {
	return &Kafka{brokers: append([]string(nil), brokers...), writer: &kafka.Writer{Addr: kafka.TCP(brokers...), Balancer: &kafka.Hash{}, RequiredAcks: kafka.RequireAll, MaxAttempts: 3, BatchSize: 1, ReadTimeout: operationTimeout, WriteTimeout: operationTimeout, AllowAutoTopicCreation: false}}
}
func (k *Kafka) Close() error { return k.writer.Close() }
func (k *Kafka) Publish(ctx context.Context, topic, key string, value []byte) error {
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	return k.writer.WriteMessages(ctx, kafka.Message{Topic: topic, Key: []byte(key), Value: append([]byte(nil), value...)})
}

// Consume runs one serial worker per assigned partition. Handlers must honor
// cancellation: a generation cannot surrender its assignments until they exit.
// Each reader has a bounded prefetch queue; a failed record stalls only its own
// partition, and a later offset never commits past that record.
func (k *Kafka) Consume(ctx context.Context, group, topic string, handler func(context.Context, []byte) error) error {
	return k.consume(ctx, group, topic, handler, false, func(int) int64 { return 0 })
}

// ConsumeStrict refuses retained suffix recovery. Use it for complete projections.
func (k *Kafka) ConsumeStrict(ctx context.Context, group, topic string, handler func(context.Context, []byte) error) error {
	return k.ConsumeStrictFrom(ctx, group, topic, 0, handler)
}

// ConsumeStrictFrom preserves the scalar snapshot boundary used by Search.
func (k *Kafka) ConsumeStrictFrom(ctx context.Context, group, topic string, start int64, handler func(context.Context, []byte) error) error {
	if start < 0 {
		return errors.New("negative snapshot replay boundary")
	}
	return k.consume(ctx, group, topic, handler, true, func(int) int64 { return start })
}

// ConsumeStrictPartitions uses independently verified snapshot boundaries. A
// missing partition has no waiver and must replay from zero. Copy the map so a
// caller cannot change the recovery contract while partition workers run.
func (k *Kafka) ConsumeStrictPartitions(ctx context.Context, group, topic string, floors map[int]int64, handler func(context.Context, []byte) error) error {
	copy := make(map[int]int64, len(floors))
	for partition, offset := range floors {
		if partition < 0 || offset < 0 {
			return errors.New("invalid partition replay boundary")
		}
		copy[partition] = offset
	}
	return k.consume(ctx, group, topic, handler, true, func(partition int) int64 { return copy[partition] })
}

func (k *Kafka) consume(ctx context.Context, group, topic string, handler func(context.Context, []byte) error, strict bool, floor func(int) int64) error {
	if handler == nil {
		return errors.New("Kafka handler required")
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	cg, err := kafka.NewConsumerGroup(kafka.ConsumerGroupConfig{
		ID: group, Brokers: k.brokers, Topics: []string{topic}, StartOffset: kafka.FirstOffset,
		Dialer: &kafka.Dialer{Timeout: operationTimeout}, Timeout: operationTimeout, JoinGroupBackoff: time.Second,
	})
	if err != nil {
		return err
	}
	defer cg.Close()
	for {
		generation, err := cg.Next(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return context.Cause(ctx)
			}
			if terminalBrokerError(err) {
				return fmt.Errorf("consumer group %s: %w", group, err)
			}
			slog.Warn("consumer group retry", "topic", topic, "error_type", fmt.Sprintf("%T", err))
			if !wait(ctx, time.Second) {
				return context.Cause(ctx)
			}
			continue
		}
		for assignedTopic, partitions := range generation.Assignments {
			for _, partition := range partitions {
				generation.Start(func(generationCtx context.Context) {
					workerCtx, stopWorker := context.WithCancel(ctx)
					stopGeneration := context.AfterFunc(generationCtx, stopWorker)
					defer stopGeneration()
					defer stopWorker()
					if err := k.consumePartition(workerCtx, generation, group, assignedTopic, partition, handler, strict, floor(partition.ID)); err != nil && workerCtx.Err() == nil {
						cancel(err)
					}
				})
			}
		}
	}
}

func (k *Kafka) consumePartition(ctx context.Context, generation *kafka.Generation, group, topic string, partition kafka.PartitionAssignment, handler func(context.Context, []byte) error, strict bool, start int64) error {
	expected := partition.Offset
	if strict && expected < start {
		expected = start
	}
	metadataRetry := consumerRetry{phase: "retained_offset", group: group, topic: topic, partition: partition.ID, offset: expected}
	if expected >= 0 {
		for ctx.Err() == nil {
			first, err := k.firstOffset(ctx, topic, partition.ID)
			if err != nil {
				if terminalBrokerError(err) {
					return fmt.Errorf("retained offset %s/%d: %w", topic, partition.ID, err)
				}
				metadataRetry.failed(ctx, err)
				if !wait(ctx, time.Second) {
					return nil
				}
				continue
			}
			metadataRetry.recovered(ctx)
			if first > expected {
				k.recordGap(ctx, group, topic, partition.ID, expected, first)
				if strict {
					return &RetentionGap{group, topic, partition.ID, expected, first}
				}
				slog.ErrorContext(ctx, "tolerant consumer continuing with incomplete retained history", "group", group, "topic", topic, "partition", partition.ID)
				expected = first
			}
			break
		}
	}
	if ctx.Err() != nil {
		return nil
	}
	r := kafka.NewReader(kafka.ReaderConfig{Brokers: k.brokers, Topic: topic, Partition: partition.ID,
		Dialer: &kafka.Dialer{Timeout: operationTimeout}, MinBytes: 1, MaxBytes: 4 << 20,
		MaxWait: time.Second, QueueCapacity: 1, ReadBackoffMin: 100 * time.Millisecond, ReadBackoffMax: time.Second})
	defer r.Close()
	if err := r.SetOffset(expected); err != nil {
		return fmt.Errorf("set partition offset: %w", err)
	}
	fetchRetry := consumerRetry{phase: "fetch", group: group, topic: topic, partition: partition.ID, offset: expected}
	for ctx.Err() == nil {
		m, err := r.FetchMessage(ctx)
		if err != nil {
			if terminalBrokerError(err) {
				return fmt.Errorf("fetch %s/%d: %w", topic, partition.ID, err)
			}
			if ctx.Err() != nil {
				return nil
			}
			fetchRetry.failed(ctx, err)
			if !wait(ctx, time.Second) {
				return nil
			}
			continue
		}
		fetchRetry.recovered(ctx)
		recordCtx := context.WithValue(ctx, metadataKey{}, Record{m.Topic, m.Partition, m.Offset})
		if expected >= 0 && m.Offset > expected {
			k.recordGap(ctx, group, topic, m.Partition, expected, m.Offset)
			if strict {
				return &RetentionGap{group, topic, m.Partition, expected, m.Offset}
			}
			slog.ErrorContext(ctx, "tolerant consumer continuing with incomplete retained history", "group", group, "topic", topic, "partition", partition.ID)
		}
		expected = m.Offset + 1
		applicationRetry := consumerRetry{phase: "application", group: group, topic: topic, partition: m.Partition, offset: m.Offset}
		for ctx.Err() == nil {
			if err = handler(recordCtx, m.Value); err == nil {
				applicationRetry.recovered(ctx)
				break
			}
			// Holding this record is deliberate: application errors cannot authorize
			// losing a durable fact. Repair the dependency and the same record retries.
			if ctx.Err() != nil {
				return nil
			}
			applicationRetry.failed(ctx, err)
			if !wait(ctx, time.Second) {
				return nil
			}
		}
		commitRetry := consumerRetry{phase: "commit", group: group, topic: topic, partition: m.Partition, offset: m.Offset}
		for ctx.Err() == nil {
			err = generation.CommitOffsets(map[string]map[int]int64{topic: {m.Partition: m.Offset + 1}})
			if err == nil {
				commitRetry.recovered(ctx)
				break
			}
			// Returning surrenders this generation; kafka-go joins a new one only
			// after all registered workers stop. These are not terminal failures.
			if errors.Is(err, kafka.IllegalGeneration) || errors.Is(err, kafka.UnknownMemberId) || errors.Is(err, kafka.RebalanceInProgress) {
				return nil
			}
			if terminalBrokerError(err) {
				return fmt.Errorf("commit %s/%d: %w", topic, partition.ID, err)
			}
			commitRetry.failed(ctx, err)
			if !wait(ctx, time.Second) {
				return nil
			}
		}
		fetchRetry.offset = expected
	}
	return nil
}

// Only explicit permanent broker rejections terminate a worker. Network,
// leader-election and storage failures remain retryable; no retry skips data.
func terminalBrokerError(err error) bool {
	for _, permanent := range []error{kafka.TopicAuthorizationFailed, kafka.GroupAuthorizationFailed, kafka.ClusterAuthorizationFailed, kafka.SASLAuthenticationFailed, kafka.UnsupportedSASLMechanism, kafka.InvalidTopic} {
		if errors.Is(err, permanent) {
			return true
		}
	}
	return false
}

type consumerRetry struct {
	phase, group, topic string
	partition           int
	offset              int64
	attempts            int
	began               time.Time
}

func (r *consumerRetry) failed(ctx context.Context, err error) {
	r.attempts++
	if r.attempts == 1 {
		r.began = time.Now()
	}
	// Error text may contain application payloads or credentials. Types and
	// numeric Kafka codes are safe diagnostics without copying arbitrary text.
	if r.attempts == 1 || r.attempts%30 == 0 {
		var code kafka.Error
		errors.As(err, &code)
		slog.WarnContext(ctx, "consumer retry holding progress", "phase", r.phase, "group", r.group, "topic", r.topic, "partition", r.partition, "offset", r.offset, "attempts", r.attempts, "elapsed", time.Since(r.began), "error_type", fmt.Sprintf("%T", err), "broker_code", int(code))
	}
}
func (r *consumerRetry) recovered(ctx context.Context) {
	if r.attempts > 0 {
		slog.InfoContext(ctx, "consumer retry recovered", "phase", r.phase, "group", r.group, "topic", r.topic, "partition", r.partition, "offset", r.offset, "attempts", r.attempts, "elapsed", time.Since(r.began))
		r.attempts = 0
	}
}
func wait(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
func (k *Kafka) client() *kafka.Client {
	return &kafka.Client{Addr: kafka.TCP(k.brokers...), Timeout: operationTimeout}
}
func (k *Kafka) Ping(ctx context.Context) error {
	if len(k.brokers) == 0 {
		return errors.New("Kafka brokers required")
	}
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	metadata, err := k.client().Metadata(ctx, &kafka.MetadataRequest{})
	if err != nil {
		return err
	}
	if len(metadata.Brokers) == 0 {
		return errors.New("Kafka returned no brokers")
	}
	return nil
}
func (k *Kafka) EnsureTopics(ctx context.Context, prefix string) error {
	if len(k.brokers) == 0 {
		return errors.New("Kafka brokers required")
	}
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	client := k.client()
	var configs []kafka.TopicConfig
	var names []string
	for _, name := range []string{"entity-state-events.v1", "task-events.v1", "task-dlq.v1"} {
		names = append(names, prefix+name)
		configs = append(configs, kafka.TopicConfig{Topic: prefix + name, NumPartitions: 3, ReplicationFactor: 1, ConfigEntries: []kafka.ConfigEntry{{ConfigName: "retention.ms", ConfigValue: "604800000"}}})
	}
	created, err := client.CreateTopics(ctx, &kafka.CreateTopicsRequest{Topics: configs})
	if err != nil {
		return err
	}
	for _, name := range names {
		if err, ok := created.Errors[name]; !ok {
			return fmt.Errorf("missing create response for %s", name)
		} else if err != nil && !errors.Is(err, kafka.TopicAlreadyExists) {
			return fmt.Errorf("create %s: %w", name, err)
		}
	}
	metadata, err := client.Metadata(ctx, &kafka.MetadataRequest{Topics: names})
	if err != nil {
		return err
	}
	counts := map[string]int{}
	for _, topic := range metadata.Topics {
		if topic.Error != nil {
			return topic.Error
		}
		for _, partition := range topic.Partitions {
			if partition.Error != nil {
				return partition.Error
			}
		}
		counts[topic.Name] = len(topic.Partitions)
	}
	for _, name := range names {
		if counts[name] != 3 {
			return fmt.Errorf("topic %s must have 3 partitions", name)
		}
	}
	return nil
}
