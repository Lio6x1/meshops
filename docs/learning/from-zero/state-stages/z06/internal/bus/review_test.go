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

func TestStrictPartitionFloorsRejectInvalidInput(t *testing.T) {
	k := New([]string{"127.0.0.1:1"})
	defer k.Close()
	for _, floors := range []map[int]int64{{-1: 0}, {0: -1}} {
		if err := k.ConsumeStrictPartitions(context.Background(), "g", "t", floors, func(context.Context, []byte) error { return nil }); err == nil {
			t.Fatal("invalid replay floor accepted")
		}
	}
}

func TestBrokerFailureClassification(t *testing.T) {
	for _, err := range []error{kafka.TopicAuthorizationFailed, kafka.GroupAuthorizationFailed, kafka.SASLAuthenticationFailed, kafka.InvalidTopic} {
		if !terminalBrokerError(err) {
			t.Fatalf("permanent broker failure retried: %v", err)
		}
	}
	for _, err := range []error{kafka.NotLeaderForPartition, kafka.RequestTimedOut, kafka.RebalanceInProgress, errors.New("connection reset")} {
		if terminalBrokerError(err) {
			t.Fatalf("transient failure terminated consumer: %v", err)
		}
	}
}

func TestStrictPerPartitionReplayBoundaries(t *testing.T) {
	addr := os.Getenv("MESHOPS_TEST_KAFKA_BROKERS")
	if addr == "" {
		t.Skip("requires real Kafka")
	}
	brokers := strings.Split(addr, ",")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	prefix := "partition_floor_" + strconv.FormatInt(time.Now().UnixNano(), 36) + "_"
	topic := prefix + "entity-state-events.v1"
	b := New(brokers)
	defer b.Close()
	if err := b.EnsureTopics(ctx, prefix); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		response, err := b.client().DeleteTopics(cleanup, &kafka.DeleteTopicsRequest{Topics: []string{topic, prefix + "task-events.v1", prefix + "task-dlq.v1"}})
		if err != nil {
			t.Error(err)
			return
		}
		for _, err := range response.Errors {
			if err != nil {
				t.Error(err)
			}
		}
	}()
	writer := &kafka.Writer{Addr: kafka.TCP(brokers...), Topic: topic, Balancer: exactPartition{}, RequiredAcks: kafka.RequireAll, BatchSize: 1}
	defer writer.Close()
	var records []kafka.Message
	for partition := 0; partition < 3; partition++ {
		for i := 0; i < 3-partition; i++ {
			records = append(records, kafka.Message{Partition: partition, Value: []byte("state")})
		}
	}
	if err := writer.WriteMessages(ctx, records...); err != nil {
		t.Fatal(err)
	}
	received := make(chan Record, 8)
	done := make(chan error, 1)
	cctx, stop := context.WithCancel(ctx)
	defer stop()
	floors := map[int]int64{0: 2, 1: 1, 2: 0}
	go func() {
		done <- b.ConsumeStrictPartitions(cctx, prefix+"projector", topic, floors, func(c context.Context, _ []byte) error {
			select {
			case received <- RecordMetadata(c):
				return nil
			case <-c.Done():
				return c.Err()
			}
		})
	}()
	seen := map[int]bool{}
	for len(seen) < 3 {
		select {
		case record := <-received:
			if record.Offset != floors[record.Partition] || seen[record.Partition] {
				t.Fatal("wrong snapshot replay floor", record)
			}
			seen[record.Partition] = true
		case <-ctx.Done():
			t.Fatal("missing partition replay", ctx.Err())
		}
	}
	stop()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("partition workers leaked")
	}
}
