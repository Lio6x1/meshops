// Package platform contains the shared configuration and trusted identities.
//
//lint:file-ignore SA5008 go-zero conf intentionally extends JSON tags with optional; LoadConfig tests exercise these tags.
package platform

import (
	"context"
	"github.com/zeromicro/go-zero/zrpc"
	"time"
)

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

// Duration is used only after LoadConfig has validated configured values.
func Duration(s string) time.Duration { d, _ := time.ParseDuration(s); return d }
