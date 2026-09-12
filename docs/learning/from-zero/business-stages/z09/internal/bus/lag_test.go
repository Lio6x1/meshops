package bus

import (
	"context"
	"errors"
	"github.com/segmentio/kafka-go"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// 处理函数被取消后，必须保留记录供新一代消费者继续处理。
// Lag 必须反映 broker 的提交状态，包括尚未保存偏移量的消费组。
func TestLagAndReplayAfterCanceledHandler(t *testing.T) {
	address := os.Getenv("MESHOPS_TEST_KAFKA_BROKERS")
	if address == "" {
		t.Skip("real Kafka test requires MESHOPS_TEST_KAFKA_BROKERS")
	}
	brokers := strings.Split(address, ",")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	prefix := "bus_replay_" + strconv.FormatInt(time.Now().UnixNano(), 36) + "_"
	topic, group := prefix+"task-events.v1", prefix+"dispatcher"
	b := New(brokers)
	defer b.Close()
	if err := b.EnsureTopics(ctx, prefix); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		response, err := b.client().DeleteTopics(cleanup, &kafka.DeleteTopicsRequest{Topics: []string{prefix + "entity-state-events.v1", topic, prefix + "task-dlq.v1"}})
		if err != nil {
			t.Error(err)
			return
		}
		for name, err := range response.Errors {
			if err != nil {
				t.Errorf("delete %s: %v", name, err)
			}
		}
	}()
	if err := b.EnsureTopics(ctx, prefix); err != nil {
		t.Fatalf("idempotent topic creation: %v", err)
	}
	for _, value := range []string{"first", "next"} {
		if err := b.Publish(ctx, topic, "same-key", []byte(value)); err != nil {
			t.Fatal(err)
		}
	}
	assertLag := func(want int64) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			got, err := b.Lag(ctx, group, topic)
			if err != nil {
				t.Fatal(err)
			}
			if got == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("lag=%d, want %d", got, want)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	assertLag(2)
	entered := make(chan Record, 1)
	consumerCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- b.Consume(consumerCtx, group, topic, func(ctx context.Context, value []byte) error {
			select {
			case entered <- RecordMetadata(ctx):
			default:
			}
			<-ctx.Done()
			return ctx.Err()
		})
	}()
	defer stop()
	var original Record
	select {
	case original = <-entered:
	case <-ctx.Done():
		t.Fatal("consumer never entered handler")
	}
	assertLag(2)
	stop()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancel did not stop consumer")
	}
	assertLag(2)
	replayCtx, stopReplay := context.WithCancel(ctx)
	defer stopReplay()
	records := make(chan Record, 4)
	replayDone := make(chan error, 1)
	go func() {
		replayDone <- b.Consume(replayCtx, group, topic, func(ctx context.Context, value []byte) error {
			select {
			case records <- RecordMetadata(ctx):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	for index := 0; index < 2; index++ {
		select {
		case record := <-records:
			if record.Topic != original.Topic || record.Partition != original.Partition || record.Offset != original.Offset+int64(index) {
				t.Fatalf("replay order: got %+v, first %+v, index %d", record, original, index)
			}
		case <-ctx.Done():
			t.Fatal("missing replay record")
		}
	}
	assertLag(0)
	stopReplay()
	select {
	case <-replayDone:
	case <-time.After(10 * time.Second):
		t.Fatal("replay consumer leaked")
	}
	if _, err := b.Lag(ctx, group, prefix+"missing"); err == nil {
		t.Fatal("missing topic returned successful lag")
	}
}
