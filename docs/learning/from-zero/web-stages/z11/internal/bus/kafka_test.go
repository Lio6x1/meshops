package bus

import (
	"context"
	"errors"
	"github.com/segmentio/kafka-go"
	"net"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type exactPartition struct{}

func (exactPartition) Balance(message kafka.Message, partitions ...int) int { return message.Partition }
func TestPartitionFailureDoesNotBlockHealthyPartition(t *testing.T) {
	address := os.Getenv("MESHOPS_TEST_KAFKA_BROKERS")
	if address == "" {
		t.Skip("real Kafka test requires MESHOPS_TEST_KAFKA_BROKERS")
	}
	brokers := strings.Split(address, ",")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	prefix := "bus_it_" + strconv.FormatInt(time.Now().UnixNano(), 36) + "_"
	topic := prefix + "entity-state-events.v1"
	group := prefix + "projector"
	b := New(brokers)
	defer b.Close()
	if err := b.EnsureTopics(ctx, prefix); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		client := &kafka.Client{Addr: kafka.TCP(brokers...)}
		if _, err := client.DeleteTopics(cleanup, &kafka.DeleteTopicsRequest{Topics: []string{topic, prefix + "task-events.v1", prefix + "task-dlq.v1"}}); err != nil {
			t.Error(err)
		}
	}()
	writer := &kafka.Writer{Addr: kafka.TCP(brokers...), Topic: topic, Balancer: exactPartition{}, RequiredAcks: kafka.RequireAll, BatchSize: 1}
	defer writer.Close()
	if err := writer.WriteMessages(ctx, kafka.Message{Partition: 0, Value: []byte("blocked-first")}, kafka.Message{Partition: 0, Value: []byte("blocked-next")}); err != nil {
		t.Fatal(err)
	}
	var unblock atomic.Bool
	entered := make(chan struct{}, 1)
	applied := make(chan Record, 10)
	consumerCtx, stopConsumer := context.WithCancel(ctx)
	defer stopConsumer()
	done := make(chan error, 1)
	go func() {
		done <- b.Consume(consumerCtx, group, topic, func(ctx context.Context, value []byte) error {
			record := RecordMetadata(ctx)
			if record.Topic != topic {
				return errors.New("metadata missing")
			}
			if record.Partition == 0 && !unblock.Load() {
				select {
				case entered <- struct{}{}:
				default:
				}
				return errors.New("storage temporarily unavailable")
			}
			select {
			case applied <- record:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("failing partition did not start")
	}
	if err := writer.WriteMessages(ctx, kafka.Message{Partition: 1, Value: []byte("healthy")}); err != nil {
		t.Fatal(err)
	}
	select {
	case record := <-applied:
		if record.Partition != 1 {
			t.Fatal("crossed failed partition", record)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("healthy partition blocked by failing partition")
	}
	// Only the healthy record may commit while partition zero keeps retrying.
	deadline := time.Now().Add(5 * time.Second)
	for {
		lag, err := b.Lag(ctx, group, topic)
		if err != nil {
			t.Fatal(err)
		}
		if lag == 2 {
			break
		}
		if lag < 2 {
			t.Fatalf("committed past failed record: lag %d", lag)
		}
		if time.Now().After(deadline) {
			t.Fatalf("healthy partition did not commit: lag %d", lag)
		}
		time.Sleep(20 * time.Millisecond)
	}
	unblock.Store(true)
	seen := map[int64]bool{}
	for len(seen) < 2 {
		select {
		case record := <-applied:
			if record.Partition == 0 {
				if record.Offset != int64(len(seen)) {
					t.Fatalf("partition order violated: offset %d after %d records", record.Offset, len(seen))
				}
				seen[record.Offset] = true
			}
		case <-ctx.Done():
			t.Fatal("recovery lost queued record")
		}
	}
	if !seen[0] || !seen[1] {
		t.Fatal("offset sequence mismatch", seen)
	}
	stopConsumer()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("consumer cancellation leaked workers")
	}
}

func TestOperationsHonorContextWithSilentBroker(t *testing.T) {
	operations := map[string]func(*Kafka, context.Context) error{
		"Ping":         func(b *Kafka, ctx context.Context) error { return b.Ping(ctx) },
		"EnsureTopics": func(b *Kafka, ctx context.Context) error { return b.EnsureTopics(ctx, "unused_") },
		"Lag":          func(b *Kafka, ctx context.Context) error { _, err := b.Lag(ctx, "unused", "unused"); return err },
	}
	for name, operation := range operations {
		t.Run(name, func(t *testing.T) { testSilentBroker(t, operation) })
	}
}

func testSilentBroker(t *testing.T, operation func(*Kafka, context.Context) error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	connections := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			connections <- conn
		}
	}()
	b := New([]string{listener.Addr().String()})
	defer b.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- operation(b, ctx) }()
	defer func() {
		select {
		case conn := <-connections:
			conn.Close()
		case <-time.After(time.Second):
		}
	}()
	select {
	case err = <-done:
		if err == nil {
			t.Fatal("silent broker reported ready")
		}
	case <-time.After(time.Second):
		t.Fatal("Kafka operation ignored caller deadline")
	}
}
