package state

import (
	"context"
	commonv1 "example.com/meshops-course/gen/common/v1"
	ingestv1 "example.com/meshops-course/gen/ingest/v1"
	"example.com/meshops-course/internal/platform"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"io"
	"math"
	"sync"
	"time"
)

type Publisher interface {
	Publish(context.Context, string, string, []byte) error
}
type bucket struct {
	tokens float64
	at     time.Time
}
type Ingest struct {
	ingestv1.UnimplementedIngestServiceServer
	cfg       platform.Settings
	registry  *platform.Registry
	publisher Publisher
	mu        sync.Mutex
	active    map[string]bool
	rates     map[string]bucket
}

func NewIngest(cfg platform.Settings, r *platform.Registry, p Publisher) *Ingest {
	if cfg.BatchSize == 0 {
		cfg.BatchSize = 100
	}
	if cfg.MaxMessageBytes == 0 {
		cfg.MaxMessageBytes = 4 << 20
	}
	return &Ingest{cfg: cfg, registry: r, publisher: p, active: map[string]bool{}, rates: map[string]bucket{}}
}
func (i *Ingest) admit(key string, n, rate int) bool {
	i.mu.Lock()
	defer i.mu.Unlock()
	now := time.Now()
	b, ok := i.rates[key]
	capacity := math.Max(float64(rate), 100)
	if !ok {
		b = bucket{tokens: capacity, at: now}
	}
	b.tokens = math.Min(capacity, b.tokens+now.Sub(b.at).Seconds()*float64(rate))
	b.at = now
	allowed := b.tokens >= float64(n)
	if allowed {
		b.tokens -= float64(n)
	}
	i.rates[key] = b
	return allowed
}
func (i *Ingest) ReportEntityStates(stream ingestv1.IngestService_ReportEntityStatesServer) error {
	ctx := stream.Context()
	principal := platform.Identity(ctx)
	if principal.Role != "source" {
		return status.Error(codes.PermissionDenied, "source role required")
	}
	source, ok := i.registry.GetSource(principal.TenantID, principal.SourceID)
	if !ok {
		return status.Error(codes.PermissionDenied, "unregistered source")
	}
	key := platform.Key(principal.TenantID, principal.SourceID)
	i.mu.Lock()
	if i.active[key] {
		i.mu.Unlock()
		return status.Error(codes.ResourceExhausted, "source already connected")
	}
	i.active[key] = true
	i.mu.Unlock()
	defer func() { i.mu.Lock(); delete(i.active, key); i.mu.Unlock() }()
	var epoch string
	var ack, resume int64
	first := true
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if req == nil || len(req.Events) == 0 || len(req.Events) > 100 || len(req.Events) > i.cfg.BatchSize || proto.Size(req) > i.cfg.MaxMessageBytes || proto.Size(req) > 4<<20 {
			return status.Error(codes.InvalidArgument, "batch size out of bounds")
		}
		if !validID(req.GatewayEpoch, 128) || req.ResumeAfterSequence < 0 || req.FirstSequence < 1 || req.FirstSequence > math.MaxInt64-int64(len(req.Events)) {
			return status.Error(codes.InvalidArgument, "invalid queue baseline")
		}
		if first {
			epoch = req.GatewayEpoch
			resume = req.ResumeAfterSequence
			ack = resume
			first = false
		}
		if req.GatewayEpoch != epoch || req.ResumeAfterSequence != resume || req.FirstSequence != ack+1 {
			return status.Error(codes.InvalidArgument, "epoch or continuous sequence mismatch")
		}
		// 在第一次对外可见的写入前，完成整批数据的校验与编码。
		payloads := make([][]byte, len(req.Events))
		events := make([]*commonv1.EntityStateEvent, len(req.Events))
		received := timestamppb.Now()
		for j, input := range req.Events {
			if input == nil {
				return status.Error(codes.InvalidArgument, "nil event")
			}
			e := proto.Clone(input).(*commonv1.EntityStateEvent)
			if e.TenantId != principal.TenantID || e.SourceId != principal.SourceID {
				return status.Error(codes.PermissionDenied, "source identity mismatch")
			}
			e.ReceivedAt = received
			if err = Validate(e, i.registry); err != nil {
				return err
			}
			authoritativeCatalog(e, i.registry)
			payloads[j], err = proto.MarshalOptions{Deterministic: true}.Marshal(e)
			if err != nil {
				return status.Error(codes.InvalidArgument, "encoding failed")
			}
			events[j] = e
		}
		rate := source.Rate
		if rate <= 0 {
			rate = 100
		}
		if !i.admit(key, len(events), rate) {
			return status.Error(codes.ResourceExhausted, "source rate limit")
		}
		response := &ingestv1.ReportEntityStatesResponse{GatewayEpoch: epoch, ConfirmedSequence: ack}
		prefix := true
		for j, e := range events {
			sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err = i.publisher.Publish(sendCtx, i.cfg.TopicPrefix+"entity-state-events.v1", platform.Key(e.TenantId, e.EntityId), payloads[j])
			cancel()
			if err != nil {
				prefix = false
				response.Errors = append(response.Errors, &ingestv1.EventError{EventId: e.EventId, Sequence: req.FirstSequence + int64(j), ErrorCode: commonv1.ErrorCode_ERROR_CODE_KAFKA_UNAVAILABLE, ErrorMessage: "Kafka publication failed"})
			} else if prefix {
				response.ConfirmedSequence = req.FirstSequence + int64(j)
			}
		}
		response.ConfirmedAt = timestamppb.Now()
		if err = stream.Send(response); err != nil {
			return err
		}
		ack = response.ConfirmedSequence
		if len(response.Errors) > 0 {
			return status.Error(codes.Unavailable, "batch publication incomplete; replay after confirmed sequence")
		}
	}
}
