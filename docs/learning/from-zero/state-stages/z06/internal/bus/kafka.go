// Package bus 明确持久化边界：发布等待 broker 确认，
// 消费只在应用处理成功后提交。
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

// Consume 为每个已分配分区运行一个串行工作协程。处理函数必须响应
// 取消：只有这些协程全部退出，本代消费者才能交还分区分配。
// 每个 reader 的预取队列都有容量上限；失败记录只阻塞自身所在
// 分区，后续偏移量不能越过该记录提交。
func (k *Kafka) Consume(ctx context.Context, group, topic string, handler func(context.Context, []byte) error) error {
	return k.consume(ctx, group, topic, handler, false, func(int) int64 { return 0 })
}

// ConsumeStrict 拒绝仅用保留后缀恢复，适用于需要完整数据的投影。
func (k *Kafka) ConsumeStrict(ctx context.Context, group, topic string, handler func(context.Context, []byte) error) error {
	return k.ConsumeStrictFrom(ctx, group, topic, 0, handler)
}

// ConsumeStrictFrom 保留 Search 使用的单一快照边界。
func (k *Kafka) ConsumeStrictFrom(ctx context.Context, group, topic string, start int64, handler func(context.Context, []byte) error) error {
	if start < 0 {
		return errors.New("negative snapshot replay boundary")
	}
	return k.consume(ctx, group, topic, handler, true, func(int) int64 { return start })
}

// ConsumeStrictPartitions 使用分别验证过的快照边界。
// 未列出的分区没有跳过历史的豁免，必须从零重放。复制映射，防止调用者
// 在分区工作协程运行期间改变恢复约定。
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
			// 保留当前记录是有意为之：应用错误不能成为
			// 丢弃持久化事实的理由。修复依赖后，仍重试同一条记录。
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
			// 返回即交还本代消费者的分配；kafka-go 仅在所有已注册工作协程
			// 停止后才加入新一代消费者。这些错误不属于终止性故障。
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

// 只有 broker 明确的永久性拒绝才终止工作协程。网络、
// 选主和存储故障仍可重试；任何重试都不能跳过数据。
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
	// 错误文本可能包含应用载荷或凭据。使用错误类型和
	// Kafka 数字错误码即可安全诊断，无需复制任意错误文本。
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
