package simulation

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

func TestModeValidationAndKeyIsolation(t *testing.T) {
	for _, mode := range []Mode{Running, Paused, Offline} {
		if !mode.Valid() {
			t.Fatal(mode)
		}
	}
	for _, mode := range []Mode{"", "stop", "RUNNING"} {
		if mode.Valid() {
			t.Fatal(mode)
		}
	}
	if key("a:b", "c", "desired") == key("a", "b:c", "desired") {
		t.Fatal("tenant/source keys collide")
	}
	if got, err := modeValue(""); err != nil || got != Running {
		t.Fatal(got, err)
	}
	if _, err := modeValue("invalid"); err == nil {
		t.Fatal("corrupt desired mode was accepted")
	}
}

// This test touches only two uniquely scoped keys and never flushes a database.
func TestRedisDesiredAndObservedLifecycle(t *testing.T) {
	addr := os.Getenv("MESHOPS_TEST_REDIS_ADDR")
	if addr == "" {
		t.Skip("requires MESHOPS_TEST_REDIS_ADDR; no Redis verification claimed")
	}
	client := redis.NewClient(&redis.Options{Addr: addr})
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tenant := fmt.Sprintf("sim-test-%d", time.Now().UnixNano())
	source := "source"
	defer client.Del(context.Background(), key(tenant, source, "desired"), key(tenant, source, "observed"))
	store := NewStore(client)
	initial, err := store.Read(ctx, tenant, source)
	if err != nil || initial.Desired != Running || initial.Connected || initial.Observation != nil {
		t.Fatal(initial, err)
	}
	if err = store.SetDesired(ctx, tenant, source, Offline); err != nil {
		t.Fatal(err)
	}
	if ttl := client.PTTL(ctx, key(tenant, source, "desired")).Val(); ttl != -1 {
		t.Fatal("desired expires", ttl)
	}
	if err = store.SetDesired(ctx, tenant, source, "invalid"); err == nil {
		t.Fatal("accepted invalid mode")
	}
	mode, err := store.Desired(ctx, tenant, source)
	if err != nil || mode != Offline {
		t.Fatal(mode, err)
	}
	o := Observation{Applied: Paused, Generated: "9007199254740993", Sent: "2", Pending: "3"}
	if err = store.Observe(ctx, tenant, source, o); err != nil {
		t.Fatal(err)
	}
	status, err := store.Read(ctx, tenant, source)
	if err != nil || !status.Connected || status.Desired != Offline || status.Observation.Applied != Paused || status.Observation.Generated != o.Generated || status.Observation.ObservedAt.IsZero() {
		t.Fatal(status, err)
	}
	if ttl := client.PTTL(ctx, key(tenant, source, "observed")).Val(); ttl <= 0 || ttl > HeartbeatTTL {
		t.Fatal("heartbeat TTL", ttl)
	}
	if err = client.Expire(ctx, key(tenant, source, "observed"), 0).Err(); err != nil {
		t.Fatal(err)
	}
	status, err = store.Read(ctx, tenant, source)
	if err != nil || status.Connected || status.Desired != Offline {
		t.Fatal(status, err)
	}
	closed := redis.NewClient(&redis.Options{Addr: addr})
	_ = closed.Close()
	if _, err = NewStore(closed).Desired(ctx, tenant, source); err == nil {
		t.Fatal("Redis failure became default running")
	}
}
