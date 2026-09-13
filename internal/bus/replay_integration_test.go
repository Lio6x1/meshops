package bus

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
)

// 在业务成功与批次提交之间取消消费者，验证重新加入同组会重放，最终不漏消息。
func TestKafkaUncommittedBatchReplaysAfterConsumerRestart(t *testing.T) {
	address := os.Getenv("MESHOPS_TEST_KAFKA_BROKERS")
	if address == "" {
		t.Skip("requires real Kafka")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	b := New(strings.Split(address, ","))
	defer b.Close()
	prefix := "batch_replay_" + strconv.FormatInt(time.Now().UnixNano(), 36) + "_"
	topic := prefix + "entity-state-events.v1"
	group := prefix + "group"
	if err := b.EnsureTopics(ctx, prefix); err != nil {
		t.Fatal(err)
	}
	defer func() {
		c, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		r, err := b.client().DeleteTopics(c, &kafka.DeleteTopicsRequest{Topics: []string{topic, prefix + "task-events.v1", prefix + "task-dlq.v1"}})
		if err != nil {
			t.Error(err)
			return
		}
		for _, err := range r.Errors {
			if err != nil {
				t.Error(err)
			}
		}
	}()
	w := &kafka.Writer{Addr: kafka.TCP(b.brokers...), Topic: topic, Balancer: exactPartition{}, RequiredAcks: kafka.RequireAll, BatchSize: 1}
	defer w.Close()
	if err := w.WriteMessages(ctx, kafka.Message{Partition: 0, Value: []byte("a")}, kafka.Message{Partition: 0, Value: []byte("b")}, kafka.Message{Partition: 0, Value: []byte("c")}); err != nil {
		t.Fatal(err)
	}
	first, stopFirst := context.WithCancel(ctx)
	defer stopFirst()
	firstOffset := int64(-1)
	_ = b.Consume(first, group, topic, func(c context.Context, _ []byte) error {
		firstOffset = RecordMetadata(c).Offset
		stopFirst()
		return nil
	})
	if firstOffset != 0 {
		t.Fatalf("first offset %d", firstOffset)
	}
	if lag, err := b.Lag(ctx, group, topic); err != nil || lag != 3 {
		t.Fatalf("cancelled batch unexpectedly committed: lag=%d err=%v", lag, err)
	}
	seen := make(chan int64, 8)
	second, stopSecond := context.WithCancel(ctx)
	defer stopSecond()
	done := make(chan error, 1)
	go func() {
		done <- b.Consume(second, group, topic, func(c context.Context, _ []byte) error { seen <- RecordMetadata(c).Offset; return nil })
	}()
	for _, want := range []int64{0, 1, 2} {
		select {
		case got := <-seen:
			if got != want {
				t.Fatalf("replay offset %d want %d", got, want)
			}
		case <-ctx.Done():
			t.Fatal("restart lost records")
		}
	}
	for {
		lag, err := b.Lag(ctx, group, topic)
		if err != nil {
			t.Fatal(err)
		}
		if lag == 0 {
			break
		}
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		time.Sleep(20 * time.Millisecond)
	}
	stopSecond()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("restart consumer leaked")
	}
}
