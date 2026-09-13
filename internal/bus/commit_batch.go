package bus

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/segmentio/kafka-go"
)

const commitBatchSize = 100
const commitBatchInterval = 100 * time.Millisecond

// consumeRecords 按分区串行处理，只合并连续成功记录的下一位点。
// 满 100 条或等待 100ms 后同步提交；空闲 Fetch 使用截止时间唤醒，
// 不额外启动提交协程，避免提交与处理进度竞争。应用/网络阻塞可能延长
// 提交间隔，但不会扩大成功未提交条数。取消时不使用旧代身份强行提交，
// 剩余记录交由下一代重放，业务处理仍必须幂等。
func (k *Kafka) consumeRecords(ctx context.Context, fetch func(context.Context) (kafka.Message, error), commit func(map[string]map[int]int64) error, group, topic string, partition int, expected int64, handler func(context.Context, []byte) error, strict bool) error {
	pending, next := 0, int64(0)
	var due time.Time
	flush := func() (bool, error) {
		if pending == 0 {
			return true, nil
		}
		retry := consumerRetry{phase: "commit", group: group, topic: topic, partition: partition, offset: next - 1}
		for ctx.Err() == nil {
			err := commit(map[string]map[int]int64{topic: {partition: next}})
			if err == nil {
				retry.recovered(ctx)
				pending = 0
				return true, nil
			}
			if errors.Is(err, kafka.IllegalGeneration) || errors.Is(err, kafka.UnknownMemberId) || errors.Is(err, kafka.RebalanceInProgress) {
				return false, nil
			}
			if terminalBrokerError(err) {
				return false, fmt.Errorf("commit %s/%d: %w", topic, partition, err)
			}
			retry.failed(ctx, err)
			if !wait(ctx, time.Second) {
				return false, nil
			}
		}
		return false, nil
	}
	fetchRetry := consumerRetry{phase: "fetch", group: group, topic: topic, partition: partition, offset: expected}
	for ctx.Err() == nil {
		if pending > 0 && (pending >= commitBatchSize || !time.Now().Before(due)) {
			if ok, err := flush(); !ok {
				return err
			}
			if ctx.Err() != nil {
				return nil
			}
		}
		fetchCtx := ctx
		cancel := func() {}
		if pending > 0 {
			fetchCtx, cancel = context.WithDeadline(ctx, due)
		}
		m, err := fetch(fetchCtx)
		idleFlush := pending > 0 && errors.Is(fetchCtx.Err(), context.DeadlineExceeded)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			if idleFlush {
				continue
			}
			if terminalBrokerError(err) {
				return fmt.Errorf("fetch %s/%d: %w", topic, partition, err)
			}
			// Fetch 故障也先保存此前成功进度，不越过尚未取得的记录。
			if ok, flushErr := flush(); !ok {
				return flushErr
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
			k.recordGap(ctx, group, topic, partition, expected, m.Offset)
			if strict {
				return &RetentionGap{group, topic, partition, expected, m.Offset}
			}
			slog.ErrorContext(ctx, "tolerant consumer continuing with incomplete retained history", "group", group, "topic", topic, "partition", partition)
		}
		applicationRetry := consumerRetry{phase: "application", group: group, topic: topic, partition: partition, offset: m.Offset}
		for {
			if ctx.Err() != nil {
				return nil
			}
			err = handler(recordCtx, m.Value)
			if err == nil {
				applicationRetry.recovered(ctx)
				break
			}
			if ctx.Err() != nil {
				return nil
			}
			// 毒消息不能永久拖住此前的成功位点；失败记录本身不进入批次。
			if ok, flushErr := flush(); !ok {
				return flushErr
			}
			applicationRetry.failed(ctx, err)
			if !wait(ctx, time.Second) {
				return nil
			}
		}
		if pending == 0 {
			due = time.Now().Add(commitBatchInterval)
		}
		pending++
		next = m.Offset + 1
		expected = next
		fetchRetry.offset = expected
	}
	return nil
}
