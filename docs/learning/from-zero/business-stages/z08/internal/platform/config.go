package platform

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/go-sql-driver/mysql"
	"github.com/zeromicro/go-zero/core/conf"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func LoadConfig(path string) (Config, error) {
	var c Config
	if err := conf.Load(path, &c); err != nil {
		return c, fmt.Errorf("service config: %w", err)
	}
	if err := localRPCAddress(c.ListenOn, true); err != nil {
		return c, fmt.Errorf("ListenOn: %w", err)
	}
	s := &c.MeshOps
	if s.Manifest == "" {
		return c, errors.New("MeshOps.Manifest is required")
	}
	paths := strings.Split(s.Manifest, ",")
	for i, p := range paths {
		p = strings.TrimSpace(p)
		if !filepath.IsAbs(p) {
			p = filepath.Join(filepath.Dir(path), p)
		}
		paths[i] = p
	}
	s.Manifest = strings.Join(paths, ",")
	if v := os.Getenv("MESHOPS_KAFKA_BROKERS"); v != "" {
		s.KafkaBrokers = strings.Split(v, ",")
	}
	if len(s.KafkaBrokers) == 0 {
		s.KafkaBrokers = []string{"localhost:9092"}
	}
	for _, p := range []struct {
		dst      *string
		env, def string
	}{{&s.RedisAddr, "MESHOPS_REDIS_ADDR", "localhost:6379"}, {&s.EntityEndpoint, "MESHOPS_ENTITY_ENDPOINT", "127.0.0.1:50052"}, {&s.TaskEndpoint, "MESHOPS_TASK_ENDPOINT", "127.0.0.1:50053"}, {&s.DispatcherEndpoint, "MESHOPS_DISPATCHER_ENDPOINT", "127.0.0.1:50054"}, {&s.IngestEndpoint, "MESHOPS_INGEST_ENDPOINT", "127.0.0.1:50051"}} {
		if v := os.Getenv(p.env); v != "" {
			*p.dst = v
		}
		if *p.dst == "" {
			*p.dst = p.def
		}
	}
	for _, endpoint := range []string{s.EntityEndpoint, s.TaskEndpoint, s.DispatcherEndpoint, s.IngestEndpoint} {
		if err := localRPCAddress(endpoint, false); err != nil {
			return c, fmt.Errorf("RPC endpoint: %w", err)
		}
	}
	for _, p := range []struct {
		dst *string
		def string
	}{{&s.UnaryTimeout, "5s"}, {&s.ShutdownTimeout, "10s"}, {&s.ReconcileInterval, "10s"}, {&s.SlowConsumerTimeout, "5s"}, {&s.OutboxPoll, "500ms"}, {&s.OutboxLease, "30s"}, {&s.AckTimeout, "30s"}, {&s.HistoryPeriod, "1s"}} {
		if *p.dst == "" {
			*p.dst = p.def
		}
		d, err := time.ParseDuration(*p.dst)
		if err != nil || d <= 0 {
			return c, errors.New("MeshOps duration must be positive")
		}
	}
	for _, p := range []struct {
		dst *int
		def int
	}{{&s.BatchSize, 100}, {&s.MaxMessageBytes, 4 << 20}, {&s.SubscriberQueueSize, 1000}, {&s.SubscriberMaxBytes, 8 << 20}, {&s.MaxAttempts, 5}, {&s.HistoryMaxEntities, 100}, {&s.HistoryBudget, 200}} {
		if *p.dst == 0 {
			*p.dst = p.def
		}
		if *p.dst < 1 {
			return c, errors.New("MeshOps capacity must be positive")
		}
	}
	if s.BatchSize > 100 || s.MaxMessageBytes > 4<<20 || s.SubscriberQueueSize > 1000 || s.SubscriberMaxBytes > 8<<20 || s.MaxAttempts > 5 {
		return c, errors.New("MeshOps capacity exceeds contract")
	}
	if len(s.RetryDelays) == 0 {
		s.RetryDelays = []string{"5s", "30s", "300s", "300s"}
	}
	for _, v := range s.RetryDelays {
		if d, e := time.ParseDuration(v); e != nil || d <= 0 {
			return c, errors.New("RetryDelays must be positive")
		}
	}
	if s.MySQLDSNEnv == "" {
		s.MySQLDSNEnv = "MESHOPS_MYSQL_DSN"
	}
	if s.CursorKeyEnv == "" {
		s.CursorKeyEnv = "MESHOPS_CURSOR_KEY"
	}
	if s.RedisPasswordEnv == "" {
		s.RedisPasswordEnv = "MESHOPS_REDIS_PASSWORD"
	}
	for _, p := range []string{s.TopicPrefix, s.ConsumerGroupPrefix} {
		if p != "" && !ValidID(p, 64) {
			return c, errors.New("invalid topic/group prefix")
		}
	}
	return c, nil
}

// Plaintext RPC is deliberately local-only. Do not resolve hostnames: an IP literal
// makes the boundary explicit and prevents resolver/scheme overrides from bypassing it.
func localRPCAddress(address string, allowZeroPort bool) error {
	host, port, err := net.SplitHostPort(address)
	ip := net.ParseIP(host)
	n, portErr := strconv.Atoi(port)
	if err != nil || ip == nil || !ip.IsLoopback() || portErr != nil || n < 0 || n > 65535 || (!allowZeroPort && n == 0) {
		return errors.New("plaintext RPC requires a loopback IP and valid port")
	}
	return nil
}

func OpenDB(env string) (*sql.DB, error) {
	raw := os.Getenv(env)
	if raw == "" {
		return nil, fmt.Errorf("%s is required", env)
	}
	cfg, err := mysql.ParseDSN(raw)
	if err != nil {
		return nil, errors.New("invalid MySQL DSN")
	}
	cfg.ParseTime = true
	cfg.Loc = time.UTC
	if cfg.Params == nil {
		cfg.Params = map[string]string{}
	}
	cfg.Params["time_zone"] = "'+00:00'"
	cfg.Timeout = 5 * time.Second
	cfg.ReadTimeout = 5 * time.Second
	cfg.WriteTimeout = 5 * time.Second
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return nil, errors.New("MySQL open failed")
	}
	db.SetMaxOpenConns(16)
	db.SetMaxIdleConns(8)
	db.SetConnMaxLifetime(5 * time.Minute)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = db.PingContext(ctx); err != nil {
		db.Close()
		return nil, errors.New("MySQL unavailable")
	}
	return db, nil
}
