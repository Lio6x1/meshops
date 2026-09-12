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
	if handler == nil {
		return errors.New("Kafka handler required")
	}
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
				return ctx.Err()
			}
			slog.Warn("consumer group retry", "topic", topic, "error", err)
			if !wait(ctx, time.Second) {
				return ctx.Err()
			}
			continue
		}
		for assignedTopic, partitions := range generation.Assignments {
			for _, partition := range partitions {
				generation.Start(func(generationCtx context.Context) {
					workerCtx, cancel := context.WithCancel(ctx)
					stop := context.AfterFunc(generationCtx, cancel)
					defer stop()
					defer cancel()
					k.consumePartition(workerCtx, generation, group, assignedTopic, partition, handler)
				})
			}
		}
	}
}

func (k *Kafka) consumePartition(ctx context.Context, generation *kafka.Generation, group, topic string, partition kafka.PartitionAssignment, handler func(context.Context, []byte) error) {
	expected := partition.Offset
	if expected >= 0 {
		for ctx.Err() == nil {
			first, err := k.firstOffset(ctx, topic, partition.ID)
			if err != nil {
				if !wait(ctx, time.Second) {
					return
				}
				continue
			}
			if first > expected {
				k.recordGap(ctx, group, topic, partition.ID, expected, first)
				expected = first
			}
			break
		}
	}
	r := kafka.NewReader(kafka.ReaderConfig{Brokers: k.brokers, Topic: topic, Partition: partition.ID,
		Dialer: &kafka.Dialer{Timeout: operationTimeout}, MinBytes: 1, MaxBytes: 4 << 20,
		MaxWait: time.Second, QueueCapacity: 1, ReadBackoffMin: 100 * time.Millisecond, ReadBackoffMax: time.Second})
	defer r.Close()
	if err := r.SetOffset(expected); err != nil {
		return
	}
	for ctx.Err() == nil {
		m, err := r.FetchMessage(ctx)
		if err != nil {
			if !wait(ctx, time.Second) {
				return
			}
			continue
		}
		recordCtx := context.WithValue(ctx, metadataKey{}, Record{m.Topic, m.Partition, m.Offset})
		if expected >= 0 && m.Offset > expected {
			k.recordGap(ctx, group, topic, m.Partition, expected, m.Offset)
		}
		expected = m.Offset + 1
		for ctx.Err() == nil {
			if err = handler(recordCtx, m.Value); err == nil {
				break
			}
			slog.Warn("consumer application retry", "topic", topic, "partition", m.Partition, "offset", m.Offset)
			if !wait(ctx, time.Second) {
				return
			}
		}
		for ctx.Err() == nil {
			// Kafka stores the NEXT offset. Generation identity fences stale owners.
			err = generation.CommitOffsets(map[string]map[int]int64{topic: {m.Partition: m.Offset + 1}})
			if err == nil {
				break
			}
			if errors.Is(err, kafka.IllegalGeneration) || errors.Is(err, kafka.UnknownMemberId) || errors.Is(err, kafka.RebalanceInProgress) {
				return
			}
			if !wait(ctx, time.Second) {
				return
			}
		}
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
