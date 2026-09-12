//go:build integration

package search

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/segmentio/kafka-go"
)

func TestCDCRebuildStartsAfterOldHistory(t *testing.T) {
	brokers := strings.Split(os.Getenv("MESHOPS_TEST_KAFKA_BROKERS"), ",")
	if brokers[0] == "" {
		t.Skip("MESHOPS_TEST_KAFKA_BROKERS required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	k := &kafka.Client{Addr: kafka.TCP(brokers...), Timeout: 5 * time.Second}
	topic := "search-rebuild-test-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	group := topic + "-group"
	r, err := k.CreateTopics(ctx, &kafka.CreateTopicsRequest{Topics: []kafka.TopicConfig{{Topic: topic, NumPartitions: 1, ReplicationFactor: 1}}})
	if err != nil || r.Errors[topic] != nil {
		t.Fatalf("create: %v %+v", err, r)
	}
	t.Cleanup(func() {
		c, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_, _ = k.DeleteTopics(c, &kafka.DeleteTopicsRequest{Topics: []string{topic}})
		_, _ = k.DeleteGroups(c, &kafka.DeleteGroupsRequest{GroupIDs: []string{group}})
	})
	w := &kafka.Writer{Addr: kafka.TCP(brokers...), Topic: topic, RequiredAcks: kafka.RequireAll, BatchSize: 1}
	defer w.Close()
	if err = w.WriteMessages(ctx, kafka.Message{Value: []byte("old, possibly invalid CDC")}); err != nil {
		t.Fatal(err)
	}
	p := cdcMaintenance{k: k, topic: topic, group: group}
	start, err := p.boundary(ctx)
	if err != nil || start != 1 {
		t.Fatalf("boundary %d %v", start, err)
	}
	if err = p.commit(ctx, start); err != nil {
		t.Fatal(err)
	}
	if err = w.WriteMessages(ctx, kafka.Message{Value: []byte("new CDC")}); err != nil {
		t.Fatal(err)
	}
	if err = p.commit(ctx, start); err == nil {
		t.Fatal("CDC advancing during snapshot was accepted")
	}
	cg, err := kafka.NewConsumerGroup(kafka.ConsumerGroupConfig{ID: group, Brokers: brokers, Topics: []string{topic}, StartOffset: kafka.FirstOffset})
	if err != nil {
		t.Fatal(err)
	}
	defer cg.Close()
	g, err := cg.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	parts := g.Assignments[topic]
	if len(parts) != 1 || parts[0].Offset != start {
		t.Fatalf("reset not honored: %+v", parts)
	}
	if _, err = p.boundary(ctx); err == nil {
		t.Fatal("active consumer allowed maintenance")
	}
	if err = p.commit(ctx, start); err == nil {
		t.Fatal("active consumer offset reset allowed")
	}
	rd := kafka.NewReader(kafka.ReaderConfig{Brokers: brokers, Topic: topic, Partition: 0, MinBytes: 1, MaxBytes: 1024})
	defer rd.Close()
	if err = rd.SetOffset(parts[0].Offset); err != nil {
		t.Fatal(err)
	}
	m, err := rd.FetchMessage(ctx)
	if err != nil || string(m.Value) != "new CDC" {
		t.Fatalf("old history leaked: %s %v", m.Value, err)
	}
}
