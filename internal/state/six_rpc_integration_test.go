package state

import (
	"bytes"
	"context"
	commonv1 "example.com/meshops-course/gen/common/v1"
	entityv1 "example.com/meshops-course/gen/entity/v1"
	ingestv1 "example.com/meshops-course/gen/ingest/v1"
	"example.com/meshops-course/internal/bus"
	"example.com/meshops-course/internal/platform"
	"fmt"
	"github.com/redis/go-redis/v9"
	"github.com/segmentio/kafka-go"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func TestSixFixturesThroughAuthenticatedRPCAndKafka(t *testing.T) {
	addr, brokers := os.Getenv("MESHOPS_TEST_REDIS_ADDR"), os.Getenv("MESHOPS_TEST_KAFKA_BROKERS")
	if addr == "" || brokers == "" {
		t.Skip("requires real Redis and Kafka")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	for _, kind := range []string{"PERSON", "DRONE", "VEHICLE", "ROBOT", "SENSOR", "FACILITY"} {
		t.Setenv("MESHOPS_"+kind+"_SOURCE_TOKEN", strings.Repeat("s", 32)+kind)
		t.Setenv("MESHOPS_"+kind+"_EXECUTOR_TOKEN", strings.Repeat("e", 32)+kind)
	}
	for _, role := range []string{"OPERATOR", "ADMIN", "TASK", "DISPATCHER"} {
		t.Setenv("MESHOPS_"+role+"_TOKEN", strings.Repeat("a", 32)+role)
	}
	registry, err := platform.LoadRegistry("../../configs/simulation.yaml")
	if err != nil {
		t.Fatal(err)
	}
	otherToken := strings.Repeat("o", 40)
	registry.Credentials["other:reader"] = otherToken
	registry.Principals[platform.Hash([]byte(otherToken))] = platform.Principal{ID: "reader", Role: "operator", TenantID: "other"}
	binding := registry.Bindings["demo_tenant:person-001"]
	binding.TenantID = "other"
	binding.SourceID = "other_source"
	registry.Bindings["other:person-001"] = binding
	source := registry.Sources["demo_tenant:personnel_sim"]
	source.TenantID = "other"
	source.ID = "other_source"
	source.Entities = map[string]string{"person-001": "person-001"}
	registry.Sources["other:other_source"] = source
	otherSourceToken := strings.Repeat("q", 40)
	registry.Credentials["other:other_source"] = otherSourceToken
	registry.Principals[platform.Hash([]byte(otherSourceToken))] = platform.Principal{ID: "other_source", Role: "source", SourceID: "other_source", TenantID: "other"}
	cache := redis.NewClient(&redis.Options{Addr: addr, DB: 13})
	defer cache.Close()
	generation := newID()
	if err = cache.SetNX(ctx, activeKey, generation, 0).Err(); err != nil {
		t.Fatal(err)
	}
	generation, err = cache.Get(ctx, activeKey).Result()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for _, b := range registry.Bindings {
			cache.Del(context.Background(), viewKey(generation, b.TenantID, b.EntityID))
		}
	}()
	prefix := "six_it_" + strings.ReplaceAll(newID(), "-", "") + "_"
	topic := prefix + "entity-state-events.v1"
	publisher := bus.New(strings.Split(brokers, ","))
	defer publisher.Close()
	if err = publisher.EnsureTopics(ctx, prefix); err != nil {
		t.Fatal(err)
	}
	defer func() {
		c, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		client := &kafka.Client{Addr: kafka.TCP(strings.Split(brokers, ",")...)}
		client.DeleteTopics(c, &kafka.DeleteTopicsRequest{Topics: []string{topic, prefix + "task-events.v1", prefix + "task-dlq.v1"}})
	}()
	entity := &Entity{redis: cache, registry: registry, subscribers: map[string]map[*subscription]struct{}{}}
	server := grpc.NewServer(grpc.UnaryInterceptor(registry.Unary()), grpc.StreamInterceptor(registry.Stream()))
	entityv1.RegisterEntityServiceServer(server, entity)
	ingestv1.RegisterIngestServiceServer(server, NewIngest(platform.Settings{TopicPrefix: prefix}, registry, publisher))
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go server.Serve(listener)
	defer server.Stop()
	defer listener.Close()
	conn, err := platform.Dial(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	cctx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- publisher.Consume(cctx, prefix+"projector", topic, entity.Project) }()
	defer func() {
		stop()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("consumer leaked")
		}
	}()
	ingest := ingestv1.NewIngestServiceClient(conn)
	queries := entityv1.NewEntityServiceClient(conn)
	events := map[string]*commonv1.EntityStateEvent{}
	for _, source := range registry.Sources {
		batch := []*commonv1.EntityStateEvent{}
		for rawID := range source.Entities {
			now := time.Now().UTC()
			raw, err := GenerateRaw(source, rawID, 1, now)
			if err != nil {
				t.Fatal(err)
			}
			raw = bytes.ReplaceAll(raw, []byte(`"battery_pct":78`), []byte(`"battery_pct":0`))
			raw = bytes.ReplaceAll(raw, []byte(`"value":23.5`), []byte(`"value":0`))
			if source.TenantID == "other" {
				raw = bytes.ReplaceAll(raw, []byte(`"on_duty":true`), []byte(`"on_duty":false`))
			}
			event, err := Normalize(raw, source, now)
			if err != nil {
				t.Fatal(err)
			}
			batch = append(batch, event)
		}
		event := batch[0]
		events[source.Adapter] = event
		token, err := registry.Credential(source.TenantID, source.ID)
		if err != nil {
			t.Fatal(err)
		}
		rpc, closeRPC := context.WithCancel(platform.Outgoing(ctx, token))
		stream, err := ingest.ReportEntityStates(rpc)
		if err != nil {
			closeRPC()
			t.Fatal(err)
		}
		err = stream.Send(&ingestv1.ReportEntityStatesRequest{GatewayEpoch: newID(), FirstSequence: 1, Events: batch})
		if err != nil {
			closeRPC()
			t.Fatal(err)
		}
		ack, err := stream.Recv()
		closeRPC()
		if err != nil || ack.ConfirmedSequence != int64(len(batch)) {
			t.Fatal("real publication not confirmed", err)
		}
	}
	operator := platform.Outgoing(ctx, os.Getenv("MESHOPS_OPERATOR_TOKEN"))
	for _, kind := range []string{"person", "drone", "vehicle", "robot", "sensor", "facility"} {
		for number := 1; number <= 5; number++ {
			var snapshot *commonv1.EntitySnapshot
			for {
				r, err := queries.GetSnapshot(operator, &entityv1.GetSnapshotRequest{EntityId: fmt.Sprintf("%s-%03d", kind, number)})
				if err != nil {
					t.Fatal(err)
				}
				if r.Found {
					snapshot = r.Snapshot
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				case <-time.After(10 * time.Millisecond):
				}
			}
			switch kind {
			case "person":
				if snapshot.Person == nil || !snapshot.Person.OnDuty || snapshot.Power != nil {
					t.Fatal("person fields corrupted")
				}
			case "drone":
				if snapshot.Power == nil || snapshot.Power.BatteryPercent == nil || snapshot.Power.GetBatteryPercent() != 0 || snapshot.Location.GetAltitude() != 25 {
					t.Fatal("drone zero/units corrupted")
				}
			case "vehicle":
				if snapshot.Velocity.Speed != 10 || snapshot.Vehicle.GetLoadKg() != 120.5 {
					t.Fatal("vehicle unit/payload corrupted")
				}
			case "robot":
				if snapshot.Power.GetBatteryPercent() != 65 || snapshot.Robot == nil {
					t.Fatal("robot battery unit corrupted")
				}
			case "sensor":
				if snapshot.Sensor == nil || snapshot.Sensor.Reading == nil || snapshot.Sensor.GetReading() != 0 {
					t.Fatal("sensor zero lost")
				}
			case "facility":
				if snapshot.Facility.GetOccupiedSlots() != 2 || snapshot.Facility.GetTotalSlots() != 8 || !snapshot.Facility.IsOpen {
					t.Fatal("facility slots corrupted")
				}
			}
		}
	}
	for {
		r, err := queries.GetSnapshot(platform.Outgoing(ctx, otherToken), &entityv1.GetSnapshotRequest{EntityId: "person-001"})
		if err != nil {
			t.Fatal(err)
		}
		if r.Found {
			if r.Snapshot.Person.OnDuty {
				t.Fatal("same entity ID leaked across tenant")
			}
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	// Exercise both stream-role and resource-scope rejection on real transport.
	for _, token := range []string{os.Getenv("MESHOPS_OPERATOR_TOKEN"), os.Getenv("MESHOPS_PERSON_SOURCE_TOKEN")} {
		c, stop := context.WithCancel(platform.Outgoing(ctx, token))
		stream, err := ingest.ReportEntityStates(c)
		if err != nil {
			stop()
			t.Fatal(err)
		}
		_ = stream.Send(&ingestv1.ReportEntityStatesRequest{GatewayEpoch: newID(), FirstSequence: 1, Events: []*commonv1.EntityStateEvent{events["drone"]}})
		_, err = stream.Recv()
		stop()
		if status.Code(err) != codes.PermissionDenied {
			t.Fatal("role/source forgery accepted", err)
		}
	}
}
