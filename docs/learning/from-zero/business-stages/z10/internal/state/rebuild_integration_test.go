package state

import (
	"context"
	"encoding/json"
	commonv1 "example.com/meshops-course/gen/common/v1"
	ingestv1 "example.com/meshops-course/gen/ingest/v1"
	"example.com/meshops-course/internal/bus"
	"example.com/meshops-course/internal/platform"
	"example.com/meshops-course/internal/testsupport"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestKafkaShadowMissingManifestDoesNotSwitch(t *testing.T) {
	brokerEnv, redisAddr := os.Getenv("MESHOPS_TEST_KAFKA_BROKERS"), os.Getenv("MESHOPS_TEST_REDIS_ADDR")
	if brokerEnv == "" || redisAddr == "" {
		t.Skip("integration requires MESHOPS_TEST_KAFKA_BROKERS and MESHOPS_TEST_REDIS_ADDR; no integration pass claimed")
	}
	brokers := strings.Split(brokerEnv, ",")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	prefix := "state_it_" + newID() + "_"
	testActiveKey := (&Entity{cfg: platform.Settings{TopicPrefix: prefix}}).activeKey()
	publisher := bus.New(brokers)
	defer publisher.Close()
	if err := publisher.EnsureTopics(ctx, prefix); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		client := &kafka.Client{Addr: kafka.TCP(brokers...)}
		response, err := client.DeleteTopics(cleanup, &kafka.DeleteTopicsRequest{Topics: []string{prefix + "entity-state-events.v1", prefix + "task-events.v1", prefix + "task-dlq.v1"}})
		if err != nil {
			t.Error("generated Kafka topic cleanup", err)
			return
		}
		for topic, err := range response.Errors {
			if err != nil {
				t.Error(topic, err)
			}
		}
	}()
	// DB14 is reserved for this recovery test; application/demo uses DB0.
	cache := redis.NewClient(&redis.Options{Addr: redisAddr, DB: 14})
	defer cache.Close()
	initial := newID()
	created, err := cache.SetNX(ctx, testActiveKey, initial, 0).Result()
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		initial, err = cache.Get(ctx, testActiveKey).Result()
		if err != nil {
			t.Fatal(err)
		}
	}
	failedGeneration, completeGeneration := newID(), newID()
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := switchView.Run(cleanup, cache, []string{testActiveKey}, completeGeneration, initial).Result(); err != nil {
			t.Error(err)
		}
		if created {
			script := redis.NewScript(`if redis.call('GET',KEYS[1])==ARGV[1] then return redis.call('DEL',KEYS[1]) end; return 0`)
			if _, err := script.Run(cleanup, cache, []string{testActiveKey}, initial).Result(); err != nil {
				t.Error(err)
			}
		}
		for _, g := range []string{failedGeneration, completeGeneration} {
			if err := cache.Del(cleanup, viewKey(g, "t", "p"), viewKey(g, "t", "cold"), "meshops:view:build:"+g, "meshops:view:bounds:"+g, "meshops:view:topic:"+g).Err(); err != nil {
				t.Error(err)
			}
		}
	}()
	registry, event := ingestFixture(t)
	binding := registry.Bindings["t:p"]
	binding.EntityID = "cold"
	registry.Bindings["t:cold"] = binding
	src := registry.Sources["t:s"]
	src.Entities["cold"] = "cold"
	registry.Sources["t:s"] = src
	now := time.Now().UTC()
	event.OccurredAt = timestamppb.New(now)
	event.ExpiresAt = timestamppb.New(now.Add(30 * time.Second))
	event.ReceivedAt = timestamppb.New(now)
	deleted := proto.Clone(event).(*commonv1.EntityStateEvent)
	deleted.EntityVersion = 2
	deleted.EventId = "delete"
	deleted.Operation = commonv1.EntityOperation_ENTITY_OPERATION_DELETE
	deleted.Snapshot = nil
	ingester := NewIngest(platform.Settings{TopicPrefix: prefix}, registry, publisher)
	stream := &reportStream{ctx: platform.WithPrincipal(ctx, platform.Principal{TenantID: "t", SourceID: "s", Role: "source"}), requests: []*ingestv1.ReportEntityStatesRequest{{GatewayEpoch: "rebuild_test", FirstSequence: 1, Events: []*commonv1.EntityStateEvent{event, deleted, event}}}}
	if err = ingester.ReportEntityStates(stream); err != nil {
		t.Fatal(err)
	}
	if len(stream.responses) != 1 || stream.responses[0].ConfirmedSequence != 3 {
		t.Fatal("real Kafka ACK", stream.responses)
	}
	entity := &Entity{redis: cache, registry: registry, cfg: platform.Settings{KafkaBrokers: brokers, TopicPrefix: prefix}}
	expected := []ExpectedState{{TenantID: "t", EntityID: "p", SourceGeneration: 1, EntityVersion: 2, Operation: "DELETE"}, {TenantID: "t", EntityID: "cold", SourceGeneration: 1, EntityVersion: 1, Operation: "UPSERT"}}
	manifest := filepath.Join(t.TempDir(), "expected.json")
	raw, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(manifest, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err = entity.Rebuild(ctx, failedGeneration, manifest); err == nil || !strings.Contains(err.Error(), "incomplete rebuild") {
		t.Fatal("cold-entity absence must fail explicitly", err)
	}
	if active, err := cache.Get(ctx, testActiveKey).Result(); err != nil || active != initial {
		t.Fatal("failed rebuild switched active", active, err)
	}
	cold := proto.Clone(event).(*commonv1.EntityStateEvent)
	cold.EntityId = "cold"
	cold.EventId = "cold_observation"
	encoded, err := proto.Marshal(cold)
	if err != nil {
		t.Fatal(err)
	}
	if err = publisher.Publish(ctx, prefix+"entity-state-events.v1", "t:cold", encoded); err != nil {
		t.Fatal(err)
	}
	if err = entity.Rebuild(ctx, completeGeneration, manifest); err != nil {
		t.Fatal(err)
	}
	if active, err := cache.Get(ctx, testActiveKey).Result(); err != nil || active != completeGeneration {
		t.Fatal("verified rebuild not activated", active, err)
	}
	if _, err = entity.projectInto(ctx, completeGeneration, event, false); err != nil {
		t.Fatal(err)
	}
	want := map[string]ExpectedState{"t:p": expected[0], "t:cold": expected[1]}
	if err = entity.VerifyGeneration(ctx, completeGeneration, want); err != nil {
		t.Fatal("ordinary replay revived tombstone", err)
	}
	t.Run("actual_retention_truncation", func(t *testing.T) {
		if os.Getenv("MESHOPS_TEST_KAFKA_DELETE_RECORDS") != "1" {
			t.Skip("requires explicit disposable-topic DeleteRecords opt-in")
		}
		topic := prefix + "entity-state-events.v1"
		client := &kafka.Client{Addr: kafka.TCP(brokers...), Timeout: 5 * time.Second}
		bounds, x := client.ListOffsets(ctx, &kafka.ListOffsetsRequest{Topics: map[string][]kafka.OffsetRequest{topic: {kafka.LastOffsetOf(0), kafka.LastOffsetOf(1), kafka.LastOffsetOf(2)}}})
		if x != nil {
			t.Fatal(x)
		}
		for _, p := range bounds.Topics[topic] {
			if p.Error != nil {
				t.Fatal(p.Error)
			}
			if p.LastOffset == 0 {
				continue
			}
			if x = testsupport.TruncateKafka(ctx, topic, p.Partition, p.LastOffset); x != nil {
				t.Fatal(x)
			}
			check, x := client.ListOffsets(ctx, &kafka.ListOffsetsRequest{Topics: map[string][]kafka.OffsetRequest{topic: {kafka.FirstOffsetOf(p.Partition)}}})
			if x != nil || len(check.Topics[topic]) != 1 || check.Topics[topic][0].FirstOffset != p.LastOffset {
				t.Fatal("truncation not confirmed", x)
			}
		}
		lost := newID()
		defer cache.Del(context.Background(), "meshops:view:build:"+lost, "meshops:view:bounds:"+lost, "meshops:view:topic:"+lost)
		if x = entity.Rebuild(ctx, lost, manifest); x == nil || !strings.Contains(x.Error(), "incomplete rebuild") {
			t.Fatal("truncated log presented as complete recovery", x)
		}
		if active, x := cache.Get(ctx, testActiveKey).Result(); x != nil || active != completeGeneration {
			t.Fatal("failed truncated rebuild replaced active view", active, x)
		}
	})
}
