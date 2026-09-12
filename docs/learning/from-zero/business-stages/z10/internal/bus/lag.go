package bus

import (
	"context"
	"errors"
	"fmt"
	"github.com/segmentio/kafka-go"
	"time"
)

// Lag 汇总主题内各分区的 broker 末尾偏移量与已提交的下一偏移量之差。
// 新消费组与 Consume 一样从保留日志的起点开始。存在保留期缺口时
// 返回 RetentionGap 错误，不能将其视为正常积压。这只是一次观测，并非覆盖
// 生产、提交和保留期变化的原子快照。
func (k *Kafka) Lag(ctx context.Context, group, topic string) (int64, error) {
	if len(k.brokers) == 0 || group == "" || topic == "" {
		return 0, errors.New("Kafka brokers, group and topic required")
	}
	ctx, cancel := context.WithTimeout(ctx, operationTimeout)
	defer cancel()
	for {
		lag, err := k.lag(ctx, group, topic)
		// 新建分区或选主期间，元数据可能已存在，但主节点
		// 尚未提供偏移量查询。在调用者的同一个截止时间内重试。
		if !errors.Is(err, kafka.NotLeaderForPartition) && !errors.Is(err, kafka.LeaderNotAvailable) {
			return lag, err
		}
		if !wait(ctx, 100*time.Millisecond) {
			return 0, ctx.Err()
		}
	}
}

func (k *Kafka) lag(ctx context.Context, group, topic string) (int64, error) {
	client := k.client()
	metadata, err := client.Metadata(ctx, &kafka.MetadataRequest{Topics: []string{topic}})
	if err != nil {
		return 0, err
	}
	var partitions []int
	var requests []kafka.OffsetRequest
	for _, entry := range metadata.Topics {
		if entry.Name != topic {
			continue
		}
		if entry.Error != nil {
			return 0, entry.Error
		}
		for _, partition := range entry.Partitions {
			if partition.Error != nil {
				return 0, partition.Error
			}
			partitions = append(partitions, partition.ID)
			requests = append(requests, kafka.FirstOffsetOf(partition.ID), kafka.LastOffsetOf(partition.ID))
		}
	}
	if len(partitions) == 0 {
		return 0, fmt.Errorf("topic %s has no partitions", topic)
	}
	offsets, err := client.ListOffsets(ctx, &kafka.ListOffsetsRequest{Topics: map[string][]kafka.OffsetRequest{topic: requests}})
	if err != nil {
		return 0, err
	}
	commits, err := client.OffsetFetch(ctx, &kafka.OffsetFetchRequest{GroupID: group, Topics: map[string][]int{topic: partitions}})
	if err != nil {
		return 0, err
	}
	if commits.Error != nil {
		return 0, commits.Error
	}
	committed := map[int]int64{}
	for _, partition := range commits.Topics[topic] {
		if partition.Error != nil {
			return 0, partition.Error
		}
		committed[partition.Partition] = partition.CommittedOffset
	}
	bounds := map[int]kafka.PartitionOffsets{}
	for _, partition := range offsets.Topics[topic] {
		if partition.Error != nil {
			return 0, fmt.Errorf("read offsets %s/%d: %w", topic, partition.Partition, partition.Error)
		}
		if partition.FirstOffset < 0 || partition.LastOffset < partition.FirstOffset {
			return 0, fmt.Errorf("invalid offsets for %s/%d", topic, partition.Partition)
		}
		bounds[partition.Partition] = partition
	}
	var lag int64
	for _, id := range partitions {
		partition, foundBounds := bounds[id]
		next, foundCommit := committed[id]
		if !foundBounds || !foundCommit {
			return 0, fmt.Errorf("missing offsets for %s/%d", topic, id)
		}
		if next < partition.FirstOffset {
			if next >= 0 {
				return 0, &RetentionGap{Group: group, Topic: topic, Partition: id, Expected: next, First: partition.FirstOffset}
			}
			next = partition.FirstOffset
		}
		if next < partition.LastOffset {
			lag += partition.LastOffset - next
		}
	}
	return lag, nil
}
