//go:build integration

package tasks

import (
	"context"
	commonv1 "example.com/meshops-course/gen/common/v1"
	taskv1 "example.com/meshops-course/gen/task/v1"
	"example.com/meshops-course/internal/bus"
	"example.com/meshops-course/internal/platform"
	"example.com/meshops-course/internal/testsupport"
	"github.com/go-sql-driver/mysql"
	"github.com/segmentio/kafka-go"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

type crashPublisher struct {
	Bus
	t *testing.T
}

func (b crashPublisher) Publish(c context.Context, topic, key string, raw []byte) error {
	if err := b.Bus.Publish(c, topic, key, raw); err != nil {
		return err
	}
	testsupport.Barrier(b.t)
	return nil
}

func TestDurableCrashChild(t *testing.T) {
	mode := os.Getenv("MESHOPS_CRASH_MODE")
	if mode == "" {
		t.Skip("helper subprocess; exercised by TestOutboxAndDispatcherForceKillDurableBoundaries")
	}
	if mode != "outbox_after_publish" && mode != "dispatch_before_offset" {
		t.Fatalf("unknown subprocess crash mode %q", mode)
	}
	db, err := platform.OpenDB("MESHOPS_CRASH_DSN")
	if err != nil {
		t.Fatal(err)
	}
	prefix := os.Getenv("MESHOPS_CRASH_PREFIX")
	b := bus.New(strings.Split(os.Getenv("MESHOPS_TEST_KAFKA_BROKERS"), ","))
	if mode == "outbox_after_publish" {
		s := &Service{db: db, cfg: platform.Settings{TopicPrefix: prefix, OutboxLease: "1s"}}
		_, err = s.publishOne(context.Background(), crashPublisher{b, t})
		t.Fatal("publisher missed crash barrier", err)
	}
	conn, err := platform.Dial(os.Getenv("MESHOPS_CRASH_ENDPOINT"))
	if err != nil {
		t.Fatal(err)
	}
	d := &Dispatcher{db: db, reg: testRegistry(), task: taskv1.NewTaskServiceClient(conn)}
	err = b.Consume(context.Background(), prefix+"dispatcher", prefix+"task-events.v1", func(c context.Context, raw []byte) error {
		if err := d.HandleEvent(c, raw); err != nil {
			return err
		}
		testsupport.Barrier(t)
		return nil
	})
	t.Fatal("consumer missed crash barrier", err)
}

func TestOutboxAndDispatcherForceKillDurableBoundaries(t *testing.T) {
	broker := os.Getenv("MESHOPS_TEST_KAFKA_BROKERS")
	if broker == "" {
		t.Fatal("real Kafka required")
	}
	f := fixtureFor(t)
	task := create(t, f, "process-crash")
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	prefix := "crash_it_" + strings.ReplaceAll(platform.NewID(), "-", "") + "_"
	topic := prefix + "task-events.v1"
	b := bus.New(strings.Split(broker, ","))
	defer b.Close()
	if err := b.EnsureTopics(ctx, prefix); err != nil {
		t.Fatal(err)
	}
	defer func() {
		c, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		client := &kafka.Client{Addr: kafka.TCP(strings.Split(broker, ",")...)}
		client.DeleteTopics(c, &kafka.DeleteTopicsRequest{Topics: []string{topic, prefix + "entity-state-events.v1", prefix + "task-dlq.v1"}})
	}()
	cfg, err := mysql.ParseDSN(os.Getenv("MESHOPS_TEST_MYSQL_DSN"))
	if err != nil {
		t.Fatal("invalid test DSN")
	}
	if err = f.db.QueryRow("SELECT DATABASE()").Scan(&cfg.DBName); err != nil {
		t.Fatal(err)
	}
	shared := []string{"MESHOPS_CRASH_DSN=" + cfg.FormatDSN(), "MESHOPS_CRASH_PREFIX=" + prefix}
	testsupport.KillAtBarrier(t, t.TempDir(), "outbox_after_publish", shared...)
	var pending int
	if err = f.db.QueryRow("SELECT COUNT(*) FROM outbox_events WHERE aggregate_id=? AND published_at IS NULL", task.TaskId).Scan(&pending); err != nil || pending != 1 {
		t.Fatal("unmarked publish fact lost", pending, err)
	}
	f.service.cfg.TopicPrefix = prefix
	// 等待真实租约自然到期；此处不使用 SQL 触发器或强制修改租约。
	deadline := time.Now().Add(5 * time.Second)
	for pending == 1 {
		if err = f.service.PublishOutbox(ctx, b); err != nil {
			t.Fatal(err)
		}
		if err = f.db.QueryRow("SELECT COUNT(*) FROM outbox_events WHERE aggregate_id=? AND published_at IS NULL", task.TaskId).Scan(&pending); err != nil {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("expired publisher lease did not recover")
		}
		time.Sleep(20 * time.Millisecond)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer(grpc.UnaryInterceptor(f.reg.Unary()))
	taskv1.RegisterTaskServiceServer(server, f.service)
	go server.Serve(listener)
	defer server.Stop()
	defer listener.Close()
	testsupport.KillAtBarrier(t, t.TempDir(), "dispatch_before_offset", append(shared, "MESHOPS_CRASH_ENDPOINT="+listener.Addr().String())...)
	if lag, err := b.Lag(ctx, prefix+"dispatcher", topic); err != nil || lag != 2 {
		t.Fatal("crashed consumer committed ahead of durable barrier", lag, err)
	}
	received := make(chan string, 4)
	done := make(chan error, 1)
	cctx, stop := context.WithCancel(ctx)
	defer func() {
		stop()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("consumer leaked")
		}
	}()
	go func() {
		done <- b.Consume(cctx, prefix+"dispatcher", topic, func(c context.Context, raw []byte) error {
			if err := f.dispatcher.HandleEvent(c, raw); err != nil {
				return err
			}
			event := new(commonv1.TaskEvent)
			if err := proto.Unmarshal(raw, event); err != nil {
				return err
			}
			received <- event.EventId
			return nil
		})
	}()
	ids := []string{}
	for len(ids) < 2 {
		select {
		case id := <-received:
			ids = append(ids, id)
		case <-ctx.Done():
			t.Fatal("recovery did not replay published events", ctx.Err())
		}
	}
	if ids[0] == "" || ids[0] != ids[1] {
		t.Fatal("outbox replay changed event identity", ids)
	}
	var count int
	if err = f.db.QueryRow("SELECT COUNT(*) FROM task_dispatches WHERE task_id=?", task.TaskId).Scan(&count); err != nil || count != 1 {
		t.Fatal("replay duplicated dispatch attempt", count, err)
	}
	if err = f.db.QueryRow("SELECT COUNT(*) FROM task_dispatches WHERE task_id=? AND command_id=? AND attempt=1 AND status='pending'", task.TaskId, task.TaskId+"-execute").Scan(&count); err != nil || count != 1 {
		t.Fatal("replay changed durable pending command", count, err)
	}
}
