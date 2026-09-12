package bus

import (
	"context"
	"errors"
	"example.com/meshops-course/internal/testsupport"
	"github.com/segmentio/kafka-go"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRetentionGapIsNotReportedAsHealthyLag(t *testing.T) {
	addr := os.Getenv("MESHOPS_TEST_KAFKA_BROKERS")
	if addr == "" || os.Getenv("MESHOPS_TEST_KAFKA_DELETE_RECORDS") != "1" {
		t.Skip("requires real Kafka and explicit disposable-topic DeleteRecords opt-in")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	b := New(strings.Split(addr, ","))
	defer b.Close()
	prefix := "retention_it_" + strconv.FormatInt(time.Now().UnixNano(), 36) + "_"
	topic, group := prefix+"entity-state-events.v1", prefix+"consumer"
	if err := b.EnsureTopics(ctx, prefix); err != nil {
		t.Fatal(err)
	}
	defer func() {
		c, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, err := b.client().DeleteTopics(c, &kafka.DeleteTopicsRequest{Topics: []string{topic, prefix + "task-events.v1", prefix + "task-dlq.v1"}})
		if err != nil {
			t.Error(err)
		}
	}()
	w := &kafka.Writer{Addr: kafka.TCP(b.brokers...), Topic: topic, Balancer: exactPartition{}, RequiredAcks: kafka.RequireAll, BatchSize: 1}
	defer w.Close()
	if err := w.WriteMessages(ctx, kafka.Message{Partition: 0, Value: []byte("lost-0")}, kafka.Message{Partition: 0, Value: []byte("lost-1")}, kafka.Message{Partition: 0, Value: []byte("retained-2")}); err != nil {
		t.Fatal(err)
	}
	response, err := b.client().OffsetCommit(ctx, &kafka.OffsetCommitRequest{GroupID: group, GenerationID: -1, Topics: map[string][]kafka.OffsetCommit{topic: {{Partition: 0, Offset: 0}}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range response.Topics[topic] {
		if p.Error != nil {
			t.Fatal(p.Error)
		}
	}
	if err = testsupport.TruncateKafka(ctx, topic, 0, 2); err != nil {
		t.Fatal(err)
	}
	bounds, err := b.client().ListOffsets(ctx, &kafka.ListOffsetsRequest{Topics: map[string][]kafka.OffsetRequest{topic: {kafka.FirstOffsetOf(0)}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(bounds.Topics[topic]) != 1 || bounds.Topics[topic][0].FirstOffset != 2 {
		t.Fatalf("DeleteRecords not confirmed: %+v", bounds)
	}
	if lag, err := b.Lag(ctx, group, topic); err == nil {
		t.Fatalf("lost committed offsets reported as healthy lag %d", lag)
	}
	if lag, err := b.Lag(ctx, group+"_new", topic); err != nil || lag != 1 {
		t.Fatal("fresh group should start at retained beginning", lag, err)
	}
	// Search 无法从截断日志重建完整投影。严格消费模式
	// 必须在应用或提交保留后缀之前停止。
	for _, strictGroup := range []string{group, group + "_new_strict"} {
		strictCtx, strictStop := context.WithTimeout(ctx, 15*time.Second)
		strictErr := b.ConsumeStrict(strictCtx, strictGroup, topic, func(context.Context, []byte) error {
			t.Error("strict consumer applied data after a retention gap")
			return nil
		})
		strictStop()
		var gap *RetentionGap
		if !errors.As(strictErr, &gap) {
			t.Fatalf("strict consumer must return retention gap: %v", strictErr)
		}
	}
	consumed := make(chan Record, 3)
	// 完整快照替代其记录边界之前的历史。即使消费组
	// 偏移量过期，也只能从该边界开始重放，不能从更早的前缀开始。
	floorCtx, floorStop := context.WithTimeout(ctx, 15*time.Second)
	floorSeen := make(chan Record, 1)
	floorErr := b.ConsumeStrictFrom(floorCtx, group+"_snapshot", topic, 2, func(c context.Context, _ []byte) error {
		floorSeen <- RecordMetadata(c)
		floorStop()
		return nil
	})
	floorStop()
	select {
	case record := <-floorSeen:
		if record.Offset != 2 {
			t.Fatal(record)
		}
	default:
		t.Fatalf("snapshot replay floor not used: %v", floorErr)
	}
	done := make(chan error, 1)
	cctx, stop := context.WithCancel(ctx)
	defer stop()
	go func() {
		done <- b.Consume(cctx, group, topic, func(c context.Context, v []byte) error { consumed <- RecordMetadata(c); return nil })
	}()
	select {
	case r := <-consumed:
		if r.Offset != 2 {
			t.Fatal(r)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		lag, err := b.Lag(ctx, group, topic)
		if err == nil && lag == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("retained record not committed", lag, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	stop()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("consumer leaked")
	}
	if b.gapDetections.Load() != 3 {
		t.Fatalf("expected persistent gap diagnostic after commit, got %d", b.gapDetections.Load())
	}
}
