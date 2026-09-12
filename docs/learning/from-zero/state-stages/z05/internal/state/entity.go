package state

import (
	"context"
	"errors"
	commonv1 "example.com/meshops-course/gen/common/v1"
	entityv1 "example.com/meshops-course/gen/entity/v1"
	"example.com/meshops-course/internal/platform"
	"fmt"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"strconv"
	"time"
)

const activeKey = "meshops:z05:view:active"

// 有意不设置 EXPIRE：删除标记和旧版本拒绝机制必须能抵御
// 任意离线重放。课程中的 Redis 配置为 noeviction。
var applyView = redis.NewScript(`
local old=redis.call('HMGET',KEYS[1],'source_id','source_generation','version','event_id','payload_hash')
if old[1] then
 if old[1]~=ARGV[1] or old[2]~=ARGV[2] then return -2 end
 local prior=tonumber(old[3]); local incoming=tonumber(ARGV[3])
 if incoming<prior then return -1 end
 if incoming==prior then
  if old[4]==ARGV[4] and old[5]==ARGV[5] then return 0 end
  return -2
 end
end
redis.call('HSET',KEYS[1],'source_id',ARGV[1],'source_generation',ARGV[2],
 'version',ARGV[3],'event_id',ARGV[4],'payload_hash',ARGV[5],
 'snapshot',ARGV[6],'updated_at',ARGV[7],'expires_at',ARGV[8],'deleted',ARGV[9])
return 1
`)

type Entity struct {
	entityv1.UnimplementedEntityServiceServer
	cfg      platform.Settings
	registry *platform.Registry
	redis    *redis.Client
}

// Z05/Z06 的投影构造函数；历史存储在后续历史课程中加入。
func NewEntity(cfg platform.Settings, r *platform.Registry, cache *redis.Client) (*Entity, error) {
	if r == nil || cache == nil {
		return nil, fmt.Errorf("registry and Redis required")
	}
	if cfg.SubscriberQueueSize == 0 {
		cfg.SubscriberQueueSize = 1000
	}
	if cfg.SubscriberMaxBytes == 0 {
		cfg.SubscriberMaxBytes = 8 << 20
	}
	if cfg.ReconcileInterval == "" {
		cfg.ReconcileInterval = "10s"
	}
	if cfg.SlowConsumerTimeout == "" {
		cfg.SlowConsumerTimeout = "5s"
	}
	e := &Entity{cfg: cfg, registry: r, redis: cache}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := cache.SetNX(ctx, e.activeKey(), newID(), 0).Err(); err != nil {
		return nil, err
	}
	return e, nil
}

// 活动命名空间按输入主题隔离。空前缀保留
// 常规演示键；临时验证主题不能借用该视图。
func (e *Entity) activeKey() string {
	if e.cfg.TopicPrefix == "" {
		return activeKey
	}
	return activeKey + ":" + e.cfg.TopicPrefix
}
func (e *Entity) generation(ctx context.Context) (string, error) {
	s, err := e.redis.Get(ctx, e.activeKey()).Result()
	if err != nil || !validID(s, 128) {
		return "", status.Error(codes.Unavailable, "active view unavailable")
	}
	return s, nil
}
func viewKey(generation, tenant, id string) string {
	return "view:" + generation + ":tenant:" + tenant + ":entity:" + id + ":snapshot"
}

type viewRecord struct {
	version, generation int64
	source              string
	snapshot            *commonv1.EntitySnapshot
	updated, expires    time.Time
	deleted             bool
}

func (e *Entity) read(ctx context.Context, generation, tenant, id string) (viewRecord, error) {
	var v viewRecord
	m, err := e.redis.HGetAll(ctx, viewKey(generation, tenant, id)).Result()
	if err != nil {
		return v, status.Error(codes.Unavailable, "Redis read failed")
	}
	if len(m) == 0 {
		return v, nil
	}
	for _, field := range []string{"source_id", "source_generation", "version", "event_id", "payload_hash", "snapshot", "updated_at", "expires_at", "deleted"} {
		if _, ok := m[field]; !ok {
			return v, status.Error(codes.Unavailable, "incomplete Redis view")
		}
	}
	if !validID(m["source_id"], 64) || !validID(m["event_id"], 128) || len(m["payload_hash"]) != 64 || !oneOf(m["deleted"], "0", "1") {
		return v, status.Error(codes.Unavailable, "corrupt Redis view identity")
	}
	v.version, err = strconv.ParseInt(m["version"], 10, 64)
	if err != nil || v.version < 1 || v.version > MaxVersion {
		return v, status.Error(codes.Unavailable, "corrupt view version")
	}
	v.generation, err = strconv.ParseInt(m["source_generation"], 10, 64)
	if err != nil || v.generation < 1 || v.generation > MaxVersion {
		return v, status.Error(codes.Unavailable, "corrupt view generation")
	}
	v.source = m["source_id"]
	v.deleted = m["deleted"] == "1"
	v.updated, err = time.Parse(time.RFC3339Nano, m["updated_at"])
	if err != nil {
		return v, status.Error(codes.Unavailable, "corrupt view time")
	}
	v.expires, err = time.Parse(time.RFC3339Nano, m["expires_at"])
	if err != nil {
		return v, status.Error(codes.Unavailable, "corrupt view expiry")
	}
	if !v.deleted {
		v.snapshot = &commonv1.EntitySnapshot{}
		if err = proto.Unmarshal([]byte(m["snapshot"]), v.snapshot); err != nil {
			return v, status.Error(codes.Unavailable, "corrupt view snapshot")
		}
	}
	return v, nil
}
func (v viewRecord) update(id string, kind entityv1.EntityUpdateKind) *entityv1.EntityUpdate {
	if v.deleted {
		kind = entityv1.EntityUpdateKind_ENTITY_UPDATE_KIND_DELETE
	}
	return &entityv1.EntityUpdate{EntityId: id, Version: v.version, Snapshot: v.snapshot, SourceId: v.source, SourceGeneration: v.generation, UpdatedAt: timestamppb.New(v.updated), ExpiresAt: timestamppb.New(v.expires), Kind: kind}
}
func queryRole(ctx context.Context) error {
	p := platform.Identity(ctx)
	if !validID(p.TenantID, 64) {
		return status.Error(codes.Unauthenticated, "tenant identity required")
	}
	if !oneOf(p.Role, "operator", "admin", "task_service") {
		return status.Error(codes.PermissionDenied, "query role required")
	}
	return nil
}
func validIDs(ids []string) error {
	if len(ids) < 1 || len(ids) > 100 {
		return status.Error(codes.InvalidArgument, "1..100 unique entity IDs required")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !validID(id, 128) || seen[id] {
			return status.Error(codes.InvalidArgument, "invalid or duplicate entity ID")
		}
		seen[id] = true
	}
	return nil
}
func (e *Entity) GetSnapshot(ctx context.Context, r *entityv1.GetSnapshotRequest) (*entityv1.GetSnapshotResponse, error) {
	if err := queryRole(ctx); err != nil {
		return nil, err
	}
	if r == nil || !validID(r.EntityId, 128) {
		return nil, status.Error(codes.InvalidArgument, "invalid entity ID")
	}
	generation, err := e.generation(ctx)
	if err != nil {
		return nil, err
	}
	return e.snapshot(ctx, generation, platform.Identity(ctx).TenantID, r.EntityId)
}
func (e *Entity) snapshot(ctx context.Context, generation, tenant, id string) (*entityv1.GetSnapshotResponse, error) {
	v, err := e.read(ctx, generation, tenant, id)
	if err != nil {
		return nil, err
	}
	out := &entityv1.GetSnapshotResponse{EntityId: id, ViewGeneration: generation}
	b, registered := e.registry.Lookup(tenant, id)
	if !registered || v.version == 0 || v.deleted {
		return out, nil
	}
	out.Found = true
	out.Version = v.version
	out.Snapshot = v.snapshot
	out.SourceId = v.source
	out.SourceGeneration = v.generation
	out.UpdatedAt = timestamppb.New(v.updated)
	out.ExpiresAt = timestamppb.New(v.expires)
	out.ExecutorId = b.ExecutorID
	return out, nil
}
func (e *Entity) BatchGetSnapshots(ctx context.Context, r *entityv1.BatchGetSnapshotsRequest) (*entityv1.BatchGetSnapshotsResponse, error) {
	if err := queryRole(ctx); err != nil {
		return nil, err
	}
	if r == nil {
		return nil, status.Error(codes.InvalidArgument, "request required")
	}
	if err := validIDs(r.EntityIds); err != nil {
		return nil, err
	}
	generation, err := e.generation(ctx)
	if err != nil {
		return nil, err
	}
	out := &entityv1.BatchGetSnapshotsResponse{}
	for _, id := range r.EntityIds {
		v, err := e.snapshot(ctx, generation, platform.Identity(ctx).TenantID, id)
		if err != nil {
			return nil, err
		}
		out.Snapshots = append(out.Snapshots, v)
	}
	return out, nil
}

// 被拒绝的记录会明确隔离，存储错误则向上传递，确保
// Kafka 封装层不会提交该记录。日志不记录消息内容。
func (e *Entity) decode(raw []byte) (*commonv1.EntityStateEvent, error) {
	v := &commonv1.EntityStateEvent{}
	if err := proto.Unmarshal(raw, v); err != nil {
		return nil, err
	}
	if err := Validate(v, e.registry); err != nil {
		return nil, err
	}
	expected := proto.Clone(v).(*commonv1.EntityStateEvent)
	authoritativeCatalog(expected, e.registry)
	if !proto.Equal(expected.Snapshot, v.Snapshot) {
		return nil, fmt.Errorf("non-authoritative capability")
	}
	return v, nil
}
func (e *Entity) Project(ctx context.Context, raw []byte) error {
	v, err := e.decode(raw)
	if err != nil {
		quarantine(ctx, "invalid_event")
		return nil
	}
	generation, err := e.generation(ctx)
	if err != nil {
		return err
	}
	_, err = e.projectInto(ctx, generation, v, true)
	return err
}
func (e *Entity) projectInto(ctx context.Context, generation string, v *commonv1.EntityStateEvent, notify bool) (int, error) {
	hash, err := contentHash(v)
	if err != nil {
		return 0, err
	}
	var payload []byte
	if v.Snapshot != nil {
		payload, err = proto.MarshalOptions{Deterministic: true}.Marshal(v.Snapshot)
		if err != nil {
			return 0, err
		}
	}
	deleted := "0"
	if v.Operation == commonv1.EntityOperation_ENTITY_OPERATION_DELETE {
		deleted = "1"
	}
	result, err := applyView.Run(ctx, e.redis, []string{viewKey(generation, v.TenantId, v.EntityId)}, v.SourceId, strconv.FormatInt(v.SourceGeneration, 10), strconv.FormatInt(v.EntityVersion, 10), v.EventId, hash, payload, v.ReceivedAt.AsTime().Format(time.RFC3339Nano), v.ExpiresAt.AsTime().Format(time.RFC3339Nano), deleted).Int()
	if err != nil {
		return 0, err
	}
	if result == -2 {
		quarantine(ctx, "version_conflict")
	}
	label := map[int]string{-2: "conflict", -1: "older", 0: "duplicate", 1: "applied"}[result]
	projectionResults.WithLabelValues(label).Inc()

	return result, nil
}

type Consumer interface {
	Consume(context.Context, string, string, func(context.Context, []byte) error) error
	ConsumeStrictPartitions(context.Context, string, string, map[int]int64, func(context.Context, []byte) error) error
}

func (e *Entity) Run(ctx context.Context, b Consumer) error {
	generation, err := e.generation(ctx)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	out := make(chan error, 2)
	// Z09 才引入经过验证的快照恢复起点；此前课程从零位点重放。
	go func() {
		out <- b.ConsumeStrictPartitions(ctx, ProjectorGroupName(e.cfg.ConsumerGroupPrefix, generation), e.cfg.TopicPrefix+"entity-state-events.v1", nil, func(c context.Context, raw []byte) error {
			event, err := e.decode(raw)
			if err != nil {
				quarantine(c, "invalid_event")
				return nil
			}
			_, err = e.projectInto(c, generation, event, true)
			return err
		})
	}()
	go func() { out <- e.watchGeneration(ctx, generation) }()
	err = <-out
	cancel()
	<-out
	if errors.Is(err, context.Canceled) {
		return ctx.Err()
	}
	return err
}
