// Package platform 提供共享配置与可信身份定义。
//
//lint:file-ignore SA5008 go-zero conf intentionally extends JSON tags with optional; LoadConfig tests exercise these tags.
package platform

import (
	"context"
	"github.com/zeromicro/go-zero/zrpc"
	"time"
)

// MaxEntityVersion 是 Redis Lua 能够精确比较的最大整数。
// 版本号与来源代次都会经过 Lua number（IEEE 754 双精度）；超过 2^53-1
// 后不同整数可能被当成同一个值，因此入口、磁盘队列和投影必须共用此上限。
const MaxEntityVersion int64 = 1<<53 - 1

type Principal struct {
	ID, TenantID, Role, SourceID, ExecutorID string
	EntityIDs                                []string
}
type Binding struct {
	TenantID, EntityID, Type, SourceID, ExecutorID string
	SourceGeneration                               int64
	Tasks                                          []string
}
type Source struct {
	TenantID, ID, Adapter, Fixture string
	Generation                     int64
	StaleAfter                     time.Duration
	Rate                           int
	Entities                       map[string]string
}
type Registry struct {
	Bindings    map[string]Binding
	Sources     map[string]Source
	Principals  map[string]Principal
	Credentials map[string]string
}

func Key(tenant, id string) string { return tenant + ":" + id }
func (r *Registry) Lookup(tenant, id string) (Binding, bool) {
	b, ok := r.Bindings[Key(tenant, id)]
	return b, ok
}
func (r *Registry) GetSource(tenant, id string) (Source, bool) {
	s, ok := r.Sources[Key(tenant, id)]
	return s, ok
}

type principalKey struct{}

func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}
func Identity(ctx context.Context) Principal { p, _ := ctx.Value(principalKey{}).(Principal); return p }

type Settings struct {
	Manifest            string   `json:",optional"`
	KafkaBrokers        []string `json:",optional"`
	TopicPrefix         string   `json:",optional"`
	ConsumerGroupPrefix string   `json:",optional"`
	RedisAddr           string   `json:",optional"`
	RedisPasswordEnv    string   `json:",optional"`
	MySQLDSNEnv         string   `json:",optional"`
	EntityEndpoint      string   `json:",optional"`
	TaskEndpoint        string   `json:",optional"`
	DispatcherEndpoint  string   `json:",optional"`
	IngestEndpoint      string   `json:",optional"`
	ServiceTokenEnv     string   `json:",optional"`
	CursorKeyEnv        string   `json:",optional"`
	MetricsAddr         string   `json:",optional"`
	UnaryTimeout        string   `json:",optional"`
	ShutdownTimeout     string   `json:",optional"`
	BatchSize           int      `json:",optional"`
	MaxMessageBytes     int      `json:",optional"`
	SubscriberQueueSize int      `json:",optional"`
	SubscriberMaxBytes  int      `json:",optional"`
	ReconcileInterval   string   `json:",optional"`
	SlowConsumerTimeout string   `json:",optional"`
	OutboxPoll          string   `json:",optional"`
	OutboxLease         string   `json:",optional"`
	AckTimeout          string   `json:",optional"`
	MaxAttempts         int      `json:",optional"`
	RetryDelays         []string `json:",optional"`
	HistoryPeriod       string   `json:",optional"`
	HistoryMaxEntities  int      `json:",optional"`
	HistoryBudget       int      `json:",optional"`
	SearchESURL         string   `json:",optional"`
	SearchBootstrap     string   `json:",optional"`
}
type Config struct {
	zrpc.RpcServerConf
	MeshOps Settings
}

// Duration 只能在 LoadConfig 已校验配置值后使用。
func Duration(s string) time.Duration { d, _ := time.ParseDuration(s); return d }
