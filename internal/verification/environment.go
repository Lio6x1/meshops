// Package verification 驱动隔离的真实进程与依赖，
// 仅作为验证工具，不提供业务规则的另一套实现。
package verification

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	entityv1 "example.com/meshops-course/gen/entity/v1"
	"example.com/meshops-course/internal/bus"
	"example.com/meshops-course/internal/platform"
	"example.com/meshops-course/internal/state"
	"example.com/meshops-course/internal/tasks"
	"github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"gopkg.in/yaml.v3"
)

type Process struct {
	Cmd          *exec.Cmd
	Done         chan error
	Log          *os.File
	Metrics      string
	Endpoint     string
	reservations []net.Listener
}
type Environment struct {
	Root, Dir, ID, Tenant, Prefix, DBName, Manifest, Operator string
	Registry                                                  *platform.Registry
	Sources                                                   []platform.Source
	DB                                                        *sql.DB
	Bus                                                       *bus.Kafka
	Redis                                                     *redis.Client
	Processes                                                 map[string]*Process
	Connections                                               []*grpc.ClientConn
	Entity                                                    entityv1.EntityServiceClient
	projectorGroup                                            string
	env                                                       map[string]string
	admin                                                     *sql.DB
}

func secret() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func address() (net.Listener, error) {
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return nil, e
	}
	return l, nil
}

// NewEnvironment 保留故障探测沿用的单类型测试夹具。
func NewEnvironment(ctx context.Context, root string, entities, sourceCount int) (*Environment, error) {
	return newEnvironment(ctx, root, entities, sourceCount, "person")
}
func newEnvironment(ctx context.Context, root string, entities, sourceCount int, profile string) (env *Environment, err error) {
	return newEnvironmentAt(ctx, root, entities, sourceCount, profile, strings.ReplaceAll(platform.NewID(), "-", ""))
}
func newEnvironmentAt(ctx context.Context, root string, entities, sourceCount int, profile, id string) (env *Environment, err error) {
	stage := "manifest_plan"
	progress(stage, 0, entities)
	defer func() {
		if err != nil {
			err = fmt.Errorf("%s: %w", stage, err)
		}
	}()
	plan, err := benchmarkSources(profile, entities, sourceCount)
	if err != nil {
		return nil, err
	}
	env = &Environment{Root: root, ID: id, Tenant: "verify_" + id, Prefix: "verify_" + id + "_", DBName: "verify_" + id, Processes: map[string]*Process{}, env: map[string]string{}}
	env.Dir = filepath.Join(root, ".local", "verification", id)
	if err = os.MkdirAll(env.Dir, 0700); err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			env.Close()
			env = nil
		}
	}()
	env.env["MESHOPS_CURSOR_KEY"] = secret()
	for _, name := range []string{"MESHOPS_KAFKA_BROKERS", "MESHOPS_REDIS_ADDR"} {
		if os.Getenv(name) == "" {
			return env, fmt.Errorf("%s required; load scripts/env.ps1", name)
		}
		env.env[name] = os.Getenv(name)
	}
	sources, executors, actors := []any{}, []any{}, []any{}
	for s, spec := range plan {
		sourceID := fmt.Sprintf("src_%s_%d", id, s)
		sourceEnv := fmt.Sprintf("VERIFY_SOURCE_%d", s)
		executorEnv := fmt.Sprintf("VERIFY_EXECUTOR_%d", s)
		env.env[sourceEnv] = secret()
		env.env[executorEnv] = secret()
		ids := map[string]string{}
		list := []string{}
		for _, eid := range spec.IDs {
			ids[eid] = eid
			list = append(list, eid)
		}
		sources = append(sources, map[string]any{"source_id": sourceID, "adapter": spec.Adapter, "source_generation": 1, "credential_env": sourceEnv, "fixture": filepath.Join(root, "testdata", "sources", spec.Adapter+".json"), "stale_after": "30s", "rate_limit_per_second": 2000, "entities": ids})
		if spec.Executable {
			executors = append(executors, map[string]any{"executor_id": fmt.Sprintf("executor_%d", s), "credential_env": executorEnv, "entity_ids": list, "supported_tasks": []string{"inspect"}})
		}
	}
	for _, role := range []string{"operator", "admin", "task_service", "dispatcher_service"} {
		key := "VERIFY_" + strings.ToUpper(role)
		env.env[key] = secret()
		actors = append(actors, map[string]any{"id": role, "role": role, "credential_env": key})
	}
	env.Operator = env.env["VERIFY_OPERATOR"]
	stage = "manifest_write"
	progress(stage, 0, entities)
	raw, e := json.Marshal(map[string]any{"tenant_id": env.Tenant, "sources": sources, "executors": executors, "actors": actors, "task_catalog": []any{map[string]any{"task_type": "inspect", "description": "Verification fixture", "parameter_schema_json": "{}"}}})
	if e != nil {
		return env, e
	}
	env.Manifest = filepath.Join(env.Dir, "manifest.json")
	if e = os.WriteFile(env.Manifest, raw, 0600); e != nil {
		return env, e
	}
	// 这些变量仅属于当前短生命周期的验证进程。
	for k, v := range env.env {
		if e = os.Setenv(k, v); e != nil {
			return env, e
		}
	}
	stage = "registry_load"
	progress(stage, 0, entities)
	env.Registry, e = platform.LoadRegistry(env.Manifest)
	if e != nil {
		return env, e
	}
	for s := 0; s < sourceCount; s++ {
		source, _ := env.Registry.GetSource(env.Tenant, fmt.Sprintf("src_%s_%d", id, s))
		env.Sources = append(env.Sources, source)
	}
	stage = "database_create"
	progress(stage, 0, entities)
	cfg, e := mysql.ParseDSN(os.Getenv("MESHOPS_MYSQL_DSN"))
	if e != nil {
		return env, errors.New("invalid MESHOPS_MYSQL_DSN")
	}
	env.admin, e = platform.OpenDB("MESHOPS_MYSQL_DSN")
	if e != nil {
		return env, e
	}
	if _, e = env.admin.ExecContext(ctx, "CREATE DATABASE `"+env.DBName+"`"); e != nil {
		return env, e
	}
	cfg.DBName = env.DBName
	env.env["MESHOPS_MYSQL_DSN"] = cfg.FormatDSN()
	os.Setenv("VERIFY_MYSQL_DSN", cfg.FormatDSN())
	env.DB, e = platform.OpenDB("VERIFY_MYSQL_DSN")
	if e != nil {
		return env, e
	}
	stage = "database_migrate"
	progress(stage, 0, entities)
	if e = tasks.Migrate(ctx, env.DB, filepath.Join(root, "migrations")); e != nil {
		return env, e
	}
	stage = "database_seed"
	progress(stage, 0, entities)
	if e = tasks.Seed(ctx, env.DB, env.Registry); e != nil {
		return env, e
	}
	stage = "topics_create"
	progress(stage, 0, entities)
	env.Bus = bus.New(strings.Split(env.env["MESHOPS_KAFKA_BROKERS"], ","))
	if e = env.Bus.EnsureTopics(ctx, env.Prefix); e != nil {
		return env, e
	}
	env.Redis = redis.NewClient(&redis.Options{Addr: env.env["MESHOPS_REDIS_ADDR"], Password: os.Getenv("MESHOPS_REDIS_PASSWORD")})
	for _, role := range []string{"ingest", "entity", "task", "dispatcher"} {
		a, e := address()
		if e != nil {
			return env, e
		}
		m, e := address()
		if e != nil {
			a.Close()
			return env, e
		}
		env.Processes[role] = &Process{Endpoint: a.Addr().String(), Metrics: m.Addr().String(), reservations: []net.Listener{a, m}}
		env.env["MESHOPS_"+strings.ToUpper(role)+"_ENDPOINT"] = a.Addr().String()
	}
	return env, nil
}
func (e *Environment) Start(ctx context.Context, role string) error {
	p := e.Processes[role]
	if p == nil {
		return errors.New("unknown service role")
	}
	if p.Cmd != nil {
		return errors.New("process already started")
	}
	mesh := map[string]any{"Manifest": e.Manifest, "KafkaBrokers": strings.Split(e.env["MESHOPS_KAFKA_BROKERS"], ","), "RedisAddr": e.env["MESHOPS_REDIS_ADDR"], "MySQLDSNEnv": "MESHOPS_MYSQL_DSN", "CursorKeyEnv": "MESHOPS_CURSOR_KEY", "MetricsAddr": p.Metrics, "TopicPrefix": e.Prefix, "ConsumerGroupPrefix": e.Prefix}
	if role == "task" {
		mesh["ServiceTokenEnv"] = "VERIFY_TASK_SERVICE"
	}
	if role == "dispatcher" {
		mesh["ServiceTokenEnv"] = "VERIFY_DISPATCHER_SERVICE"
	}
	config, err := yaml.Marshal(map[string]any{"Name": "verify." + role, "ListenOn": p.Endpoint, "Mode": "test", "MeshOps": mesh})
	if err != nil {
		return err
	}
	path := filepath.Join(e.Dir, role+".yaml")
	if err = os.WriteFile(path, config, 0600); err != nil {
		return err
	}
	name := role
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	command := exec.Command(filepath.Join(e.Root, "bin", name), "-f", path)
	hide(command)
	command.Dir = e.Root
	command.Env = os.Environ()
	for k, v := range e.env {
		command.Env = append(command.Env, k+"="+v)
	}
	log, err := os.OpenFile(filepath.Join(e.Dir, role+".log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	command.Stdout = log
	command.Stderr = log
	// 预留后续每个服务的端口，直到对应子进程准备绑定。
	// 若不预留，先启动的服务可能将这些端口用于出站连接。
	for _, l := range p.reservations {
		l.Close()
	}
	p.reservations = nil
	if err = command.Start(); err != nil {
		log.Close()
		return err
	}
	p.Cmd = command
	p.Log = log
	p.Done = make(chan error, 1)
	go func() { p.Done <- command.Wait() }()
	client := &http.Client{Timeout: 3 * time.Second}
	limit := time.NewTimer(120 * time.Second)
	defer limit.Stop()
	for {
		select {
		case err := <-p.Done:
			p.Cmd = nil
			log.Close()
			return fmt.Errorf("%s exited before ready: %v", role, err)
		case <-ctx.Done():
			return ctx.Err()
		case <-limit.C:
			return fmt.Errorf("%s readiness deadline", role)
		default:
		}
		response, err := client.Get("http://" + p.Metrics + "/readyz")
		if err == nil {
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
			if response.StatusCode == 200 {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}
func (e *Environment) Connect() error {
	c, err := platform.Dial(e.Processes["entity"].Endpoint)
	if err != nil {
		return err
	}
	e.Connections = append(e.Connections, c)
	e.Entity = entityv1.NewEntityServiceClient(c)
	// 在 Redis 正常时缓存命名空间。故障探测会刻意在 Redis 中断期间查询
	// Kafka 积压量，此时重新解析命名空间会失败。
	ctx, cancel := context.WithTimeout(platform.Outgoing(context.Background(), e.Operator), 5*time.Second)
	defer cancel()
	ids := sourceRawIDs(e.Sources[0])
	snapshot, err := e.Entity.GetSnapshot(ctx, &entityv1.GetSnapshotRequest{EntityId: e.Sources[0].Entities[ids[0]]})
	if err != nil {
		return err
	}
	if !platform.ValidID(snapshot.ViewGeneration, 128) {
		return errors.New("entity returned invalid view generation")
	}
	e.projectorGroup = state.ProjectorGroupName(e.Prefix, snapshot.ViewGeneration)
	return nil
}
func (e *Environment) Stop(role string) {
	p := e.Processes[role]
	if p == nil {
		return
	}
	for _, l := range p.reservations {
		l.Close()
	}
	p.reservations = nil
	if p.Cmd == nil {
		return
	}
	_ = p.Cmd.Process.Kill()
	select {
	case <-p.Done:
	case <-time.After(10 * time.Second):
	}
	p.Log.Close()
	p.Cmd = nil
}
func (e *Environment) Close() {
	for role := range e.Processes {
		e.Stop(role)
	}
	for _, c := range e.Connections {
		c.Close()
	}
	if e.Bus != nil {
		e.Bus.Close()
	}
	if e.Redis != nil {
		e.Redis.Close()
	}
	if e.DB != nil {
		e.DB.Close()
	}
	if e.admin != nil {
		e.admin.Close()
	}
	// 刻意保留命名空间内的事实供检查；绝不能清除共享的
	// Redis 活动指针，或其他运行使用的数据库和主题。
}
