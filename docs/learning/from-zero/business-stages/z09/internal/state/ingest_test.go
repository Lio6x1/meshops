package state

import (
	"context"
	"errors"
	commonv1 "example.com/meshops-course/gen/common/v1"
	ingestv1 "example.com/meshops-course/gen/ingest/v1"
	"example.com/meshops-course/internal/platform"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"io"
	"sync"
	"testing"
	"time"
)

type recordPublisher struct {
	calls int
	fail  int
}

func (p *recordPublisher) Publish(_ context.Context, _, _ string, _ []byte) error {
	p.calls++
	if p.calls == p.fail {
		return errors.New("broker down")
	}
	return nil
}

type reportStream struct {
	grpc.ServerStream
	ctx       context.Context
	requests  []*ingestv1.ReportEntityStatesRequest
	responses []*ingestv1.ReportEntityStatesResponse
}

func (s *reportStream) Context() context.Context { return s.ctx }
func (s *reportStream) Recv() (*ingestv1.ReportEntityStatesRequest, error) {
	if len(s.requests) == 0 {
		return nil, io.EOF
	}
	r := s.requests[0]
	s.requests = s.requests[1:]
	return r, nil
}
func (s *reportStream) Send(r *ingestv1.ReportEntityStatesResponse) error {
	s.responses = append(s.responses, r)
	return nil
}
func ingestFixture(t *testing.T) (*platform.Registry, *commonv1.EntityStateEvent) {
	t.Helper()
	src := platform.Source{TenantID: "t", ID: "s", Adapter: "person", Generation: 1, Rate: 1000, StaleAfter: 30 * time.Second, Entities: map[string]string{"p": "p"}}
	e, err := Normalize([]byte(`{"employee_id":"p","observation_id":"e","version":1,"observed_at":"2026-09-05T00:00:00Z","on_duty":false,"availability":"idle","skills":[]}`), src, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return &platform.Registry{Sources: map[string]platform.Source{"t:s": src}, Bindings: map[string]platform.Binding{"t:p": {TenantID: "t", EntityID: "p", Type: "person", SourceID: "s", SourceGeneration: 1}}}, e
}
func TestWholeBatchAndPrefixACK(t *testing.T) {
	r, e := ingestFixture(t)
	ctx := platform.WithPrincipal(context.Background(), platform.Principal{TenantID: "t", Role: "source", SourceID: "s"})
	p := &recordPublisher{fail: 2}
	in := NewIngest(platform.Settings{}, r, p)
	stream := &reportStream{ctx: ctx, requests: []*ingestv1.ReportEntityStatesRequest{{GatewayEpoch: "epoch", FirstSequence: 1, Events: []*commonv1.EntityStateEvent{e, e, e}}}}
	if err := in.ReportEntityStates(stream); status.Code(err) != codes.Unavailable {
		t.Fatal(err)
	}
	if p.calls != 3 || len(stream.responses) != 1 || stream.responses[0].ConfirmedSequence != 1 || stream.responses[0].Errors[0].Sequence != 2 {
		t.Fatal(p, stream.responses)
	}
	bad := proto.Clone(e).(*commonv1.EntityStateEvent)
	bad.EntityVersion = -1
	p.calls = 0
	stream = &reportStream{ctx: ctx, requests: []*ingestv1.ReportEntityStatesRequest{{GatewayEpoch: "epoch", FirstSequence: 1, Events: []*commonv1.EntityStateEvent{e, bad}}}}
	if err := in.ReportEntityStates(stream); status.Code(err) != codes.InvalidArgument || p.calls != 0 {
		t.Fatal(err, p.calls)
	}
}
func TestResumeAndEpoch(t *testing.T) {
	r, e := ingestFixture(t)
	ctx := platform.WithPrincipal(context.Background(), platform.Principal{TenantID: "t", Role: "source", SourceID: "s"})
	p := &recordPublisher{}
	in := NewIngest(platform.Settings{}, r, p)
	stream := &reportStream{ctx: ctx, requests: []*ingestv1.ReportEntityStatesRequest{{GatewayEpoch: "epoch", FirstSequence: 11, ResumeAfterSequence: 10, Events: []*commonv1.EntityStateEvent{e}}, {GatewayEpoch: "other", FirstSequence: 12, ResumeAfterSequence: 10, Events: []*commonv1.EntityStateEvent{e}}}}
	if err := in.ReportEntityStates(stream); status.Code(err) != codes.InvalidArgument {
		t.Fatal(err)
	}
	if len(stream.responses) != 1 || stream.responses[0].ConfirmedSequence != 11 || p.calls != 1 {
		t.Fatal(stream.responses, p.calls)
	}
}

type heldReportStream struct {
	reportStream
	entered chan struct{}
	once    sync.Once
}

func (s *heldReportStream) Recv() (*ingestv1.ReportEntityStatesRequest, error) {
	s.once.Do(func() { close(s.entered) })
	<-s.ctx.Done()
	return nil, s.ctx.Err()
}
func TestSingleSourceStreamAndPersistentRateBudget(t *testing.T) {
	r, event := ingestFixture(t)
	principal := platform.Principal{TenantID: "t", Role: "source", SourceID: "s"}
	ctx, cancel := context.WithCancel(platform.WithPrincipal(context.Background(), principal))
	publisher := &recordPublisher{}
	ingest := NewIngest(platform.Settings{}, r, publisher)
	held := &heldReportStream{reportStream: reportStream{ctx: ctx}, entered: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- ingest.ReportEntityStates(held) }()
	select {
	case <-held.entered:
	case <-time.After(time.Second):
		t.Fatal("first stream did not enter")
	}
	second := &reportStream{ctx: ctx}
	if err := ingest.ReportEntityStates(second); status.Code(err) != codes.ResourceExhausted {
		t.Fatal("concurrent source accepted", err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("source slot was not released")
	}
	source := r.Sources["t:s"]
	source.Rate = 1
	r.Sources["t:s"] = source
	principalCtx := platform.WithPrincipal(context.Background(), principal)
	events := make([]*commonv1.EntityStateEvent, 100)
	for index := range events {
		events[index] = event
	}
	batch := &reportStream{ctx: principalCtx, requests: []*ingestv1.ReportEntityStatesRequest{{GatewayEpoch: "epoch", FirstSequence: 1, Events: events}}}
	if err := ingest.ReportEntityStates(batch); err != nil {
		t.Fatal(err)
	}
	reconnected := &reportStream{ctx: principalCtx, requests: []*ingestv1.ReportEntityStatesRequest{{GatewayEpoch: "epoch", ResumeAfterSequence: 100, FirstSequence: 101, Events: []*commonv1.EntityStateEvent{event}}}}
	if err := ingest.ReportEntityStates(reconnected); status.Code(err) != codes.ResourceExhausted {
		t.Fatal("reconnect reset rate budget", err)
	}
	if publisher.calls != 100 {
		t.Fatal("rate-limited event published", publisher.calls)
	}
}
