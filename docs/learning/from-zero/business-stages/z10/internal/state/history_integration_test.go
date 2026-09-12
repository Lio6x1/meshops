package state

import (
	"context"
	commonv1 "example.com/meshops-course/gen/common/v1"
	entityv1 "example.com/meshops-course/gen/entity/v1"
	"example.com/meshops-course/internal/platform"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"os"
	"testing"
	"time"
)

func TestMySQLSampleIdempotencePagingAgeAndBudget(t *testing.T) {
	if os.Getenv("MESHOPS_TEST_MYSQL_DSN") == "" {
		t.Skip("integration requires MESHOPS_TEST_MYSQL_DSN and migrations001..003; no integration pass claimed")
	}
	db, err := platform.OpenDB("MESHOPS_TEST_MYSQL_DSN")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r, event := ingestFixture(t)
	tenant := "state_test_" + newID()
	source := r.Sources["t:s"]
	source.TenantID = tenant
	binding := r.Bindings["t:p"]
	binding.TenantID = tenant
	r.Sources = map[string]platform.Source{platform.Key(tenant, "s"): source}
	r.Bindings = map[string]platform.Binding{platform.Key(tenant, "p"): binding}
	event.TenantId = tenant
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := db.ExecContext(cleanup, "DELETE FROM history_sample_keys WHERE tenant_id=?", tenant); err != nil {
			t.Error(err)
		}
		if _, err := db.ExecContext(cleanup, "DELETE FROM entity_history_samples WHERE tenant_id=?", tenant); err != nil {
			t.Error(err)
		}
	}()
	e := &Entity{db: db, registry: r, cfg: platform.Settings{HistoryPeriod: "1s", HistoryBudget: 200}, cursorKey: []byte("0123456789abcdef0123456789abcdef"), historyLast: map[string]*commonv1.EntityStateEvent{}, historyAllowed: map[string]bool{platform.Key(tenant, "p"): true}}
	now := time.Now().UTC().Truncate(time.Microsecond)
	makeEvent := func(version int64, at time.Time, availability string) *commonv1.EntityStateEvent {
		v := proto.Clone(event).(*commonv1.EntityStateEvent)
		v.EntityVersion = version
		v.EventId = newID()
		v.OccurredAt = timestamppb.New(at)
		v.ExpiresAt = timestamppb.New(at.Add(30 * time.Second))
		v.ReceivedAt = timestamppb.New(now)
		v.Snapshot.Status = availability
		v.Snapshot.Person.Availability = availability
		return v
	}
	sample := func(v *commonv1.EntityStateEvent) {
		raw, err := proto.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		if err = e.Sample(ctx, raw); err != nil {
			t.Fatal(err)
		}
	}
	first := makeEvent(1, now.Add(-time.Second), "idle")
	sample(first)
	sample(first)
	second := makeEvent(2, now, "idle")
	sample(second)
	third := makeEvent(3, now, "busy")
	sample(third)
	var count int
	if err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM entity_history_samples WHERE tenant_id=?", tenant).Scan(&count); err != nil || count != 3 {
		t.Fatal("idempotent samples", count, err)
	}
	operator := platform.WithPrincipal(ctx, platform.Principal{TenantID: tenant, Role: "operator"})
	request := &entityv1.ListHistorySamplesRequest{EntityId: "p", StartTime: timestamppb.New(now.Add(-10 * time.Second)), EndTime: timestamppb.New(now.Add(10 * time.Second)), PageSize: 1}
	seen := map[string]bool{}
	var firstCursor string
	for {
		page, err := e.ListHistorySamples(operator, request)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Samples) != 1 {
			t.Fatal(page)
		}
		id := page.Samples[0].SampleId
		if seen[id] {
			t.Fatal("duplicate page item", id)
		}
		seen[id] = true
		if firstCursor == "" {
			firstCursor = page.NextPageToken
		}
		if page.NextPageToken == "" {
			break
		}
		request.PageToken = page.NextPageToken
	}
	if len(seen) != 3 {
		t.Fatal("pagination lost samples", seen)
	}
	request.PageToken = firstCursor
	other := platform.WithPrincipal(ctx, platform.Principal{TenantID: "other_tenant", Role: "operator"})
	if _, err = e.ListHistorySamples(other, request); status.Code(err) != codes.InvalidArgument {
		t.Fatal("cross-tenant cursor accepted", err)
	}
	request.PageToken = firstCursor + "x"
	if _, err = e.ListHistorySamples(operator, request); status.Code(err) != codes.InvalidArgument {
		t.Fatal("tampered cursor", err)
	}
	sample(makeEvent(4, now.Add(-8*24*time.Hour), "offline"))
	e.cfg.HistoryBudget = 1
	e.budgetSecond = time.Now().Unix()
	e.budgetTotal = 1
	sample(makeEvent(5, now.Add(time.Second), "offline"))
	if err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM entity_history_samples WHERE tenant_id=?", tenant).Scan(&count); err != nil || count != 3 {
		t.Fatal("age/budget must drop only candidates", count, err)
	}
	// A restart hydrates the last persisted sample and still rejects its replay.
	e.historyLast = map[string]*commonv1.EntityStateEvent{}
	sample(third)
	if err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM history_sample_keys WHERE tenant_id=?", tenant).Scan(&count); err != nil || count != 3 {
		t.Fatal(count, err)
	}
}
