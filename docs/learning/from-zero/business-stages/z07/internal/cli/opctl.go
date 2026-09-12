// Package cli implements the real operator client. It never fabricates RPC results.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	commonv1 "example.com/meshops-course/gen/common/v1"
	dispatcherv1 "example.com/meshops-course/gen/dispatcher/v1"
	entityv1 "example.com/meshops-course/gen/entity/v1"
	taskv1 "example.com/meshops-course/gen/task/v1"
	"example.com/meshops-course/internal/bus"
	"example.com/meshops-course/internal/platform"
	"example.com/meshops-course/internal/tasks"
	"flag"
	"fmt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"io"
	"os"
	"strings"
	"time"
)

func envDefault(name, value string) string {
	if s := os.Getenv(name); s != "" {
		return s
	}
	return value
}
func WriteJSON(out io.Writer, v any) error { return json.NewEncoder(out).Encode(v) }
func WriteProto(out io.Writer, v proto.Message) error {
	b, e := protojson.Marshal(v)
	if e != nil {
		return e
	}
	_, e = fmt.Fprintln(out, string(b))
	return e
}
func Opctl(ctx context.Context, args []string, out, diagnostics io.Writer) int {
	usage := func(msg string) int { fmt.Fprintln(diagnostics, msg); return 2 }
	failure := func(e error) int {
		code := status.Code(e)
		if code == codes.Unknown {
			code = codes.Internal
		}
		if errors.Is(e, context.Canceled) || errors.Is(e, context.DeadlineExceeded) {
			code = status.FromContextError(e).Code()
		}
		WriteJSON(diagnostics, map[string]any{"error": map[string]string{"code": code.String(), "message": e.Error()}})
		return 1
	}
	if len(args) == 0 {
		return usage("usage: opctl snapshot|subscribe|task|dispatch|dispatcher|seed")
	}
	command := args[0]
	args = args[1:]
	if command == "task" || command == "dispatch" || command == "dispatcher" {
		if len(args) == 0 {
			return usage("subcommand required")
		}
		command += " " + args[0]
		args = args[1:]
	}
	allowed := map[string]bool{"snapshot": true, "subscribe": true, "seed": true, "task create": true, "task get": true, "task list": true, "task history": true, "task cancel": true, "dispatch get": true, "dispatch retry-dlq": true, "dispatcher status": true}
	if !allowed[command] {
		return usage("unknown command")
	}
	fs := flag.NewFlagSet("opctl "+command, flag.ContinueOnError)
	fs.SetOutput(diagnostics)
	endpointDefault := envDefault("MESHOPS_ENTITY_ENDPOINT", "127.0.0.1:50052")
	if strings.HasPrefix(command, "task ") {
		endpointDefault = envDefault("MESHOPS_TASK_ENDPOINT", "127.0.0.1:50053")
	}
	if strings.HasPrefix(command, "dispatch") {
		endpointDefault = envDefault("MESHOPS_DISPATCHER_ENDPOINT", "127.0.0.1:50054")
	}
	endpoint := fs.String("endpoint", endpointDefault, "RPC endpoint")
	tokenEnv := fs.String("token-env", "MESHOPS_OPERATOR_TOKEN", "credential environment name")
	entity := fs.String("entity", "", "entity ID")
	entities := fs.String("entities", "", "comma separated entity IDs")
	id := fs.String("id", "", "task ID")
	taskID := fs.String("task", "", "task ID for dispatch query")
	dispatchID := fs.String("dispatch", "", "specific attempt ID")
	kind := fs.String("type", "inspect", "task type")
	seconds := fs.Int64("duration-seconds", 5, "inspect duration 1..60")
	note := fs.String("note", "", "inspect note")
	key := fs.String("key", "", "idempotency key")
	reason := fs.String("reason", "", "reason")
	deadline := fs.String("deadline", "", "RFC3339 deadline")
	statusFilter := fs.String("status", "", "task status")
	pageSize := fs.Int("page-size", 0, "page size")
	pageToken := fs.String("page-token", "", "signed cursor")
	duration := fs.Duration("duration", 30*time.Second, "subscription duration")
	manifest := fs.String("manifest", "configs/simulation.yaml", "identity manifest")
	dsnEnv := fs.String("dsn-env", "MESHOPS_MYSQL_DSN", "database environment name")
	migrations := fs.String("migrations", "migrations", "SQL migration directory")
	brokers := fs.String("brokers", envDefault("MESHOPS_KAFKA_BROKERS", "localhost:19092"), "Kafka brokers")
	topicPrefix := fs.String("topic-prefix", "", "Kafka topic prefix")
	if e := fs.Parse(args); e != nil {
		return 2
	}
	if fs.NArg() != 0 {
		return usage("unexpected positional arguments")
	}
	// Validate required CLI arguments before connecting.
	switch command {
	case "snapshot", "task create":
		if strings.TrimSpace(*entity) == "" {
			return usage("--entity required")
		}
	case "subscribe":
		if *entities == "" || *duration <= 0 {
			return usage("--entities and positive --duration required")
		}
	case "task get", "task history", "task cancel":
		if strings.TrimSpace(*id) == "" {
			return usage("--id required")
		}
	case "dispatch get", "dispatch retry-dlq":
		if strings.TrimSpace(*taskID) == "" {
			return usage("--task required")
		}
	}
	if command == "task create" && strings.TrimSpace(*key) == "" {
		return usage("--key required")
	}
	if (command == "task cancel" || command == "dispatch retry-dlq") && strings.TrimSpace(*reason) == "" {
		return usage("--reason required")
	}
	if strings.TrimSpace(*endpoint) == "" || strings.TrimSpace(*tokenEnv) == "" {
		return usage("--endpoint and --token-env must not be empty")
	}
	var subscriptionIDs []string
	if command == "subscribe" {
		subscriptionIDs = strings.Split(*entities, ",")
		if len(subscriptionIDs) > 100 {
			return usage("--entities requires 1..100 IDs")
		}
		seen := map[string]bool{}
		for i, value := range subscriptionIDs {
			value = strings.TrimSpace(value)
			if value == "" || seen[value] {
				return usage("--entities requires nonempty, distinct IDs")
			}
			seen[value] = true
			subscriptionIDs[i] = value
		}
	}
	var taskDeadline *timestamppb.Timestamp
	parseTimestamp := func(value string) (*timestamppb.Timestamp, error) {
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			return nil, err
		}
		stamp := timestamppb.New(parsed)
		return stamp, stamp.CheckValid()
	}
	if command == "task create" {
		if *seconds < 1 || *seconds > 60 || len(*note) > 256 {
			return usage("inspect requires --duration-seconds 1..60 and --note at most 256 bytes")
		}
		if *deadline != "" {
			var err error
			if taskDeadline, err = parseTimestamp(*deadline); err != nil {
				return usage("--deadline requires RFC3339")
			}
		}
	}
	st := commonv1.TaskStatus_TASK_STATUS_UNSPECIFIED
	if command == "task list" && *statusFilter != "" {
		name := strings.ToUpper(*statusFilter)
		if !strings.HasPrefix(name, "TASK_STATUS_") {
			name = "TASK_STATUS_" + name
		}
		v, ok := commonv1.TaskStatus_value[name]
		if !ok {
			return usage("unknown --status")
		}
		st = commonv1.TaskStatus(v)
	}
	if command == "task list" {
		limit := 100
		if *pageSize < 0 || *pageSize > limit {
			return usage(fmt.Sprintf("--page-size must be 0..%d", limit))
		}
	}
	token := os.Getenv(*tokenEnv)
	if len(token) < 32 {
		return failure(status.Error(codes.Unauthenticated, "credential environment is missing or too short"))
	}
	if command == "seed" {
		reg, e := platform.LoadRegistry(*manifest)
		if e != nil {
			return failure(e)
		}
		p, e := reg.Authenticate(token)
		if e != nil || p.Role != "admin" {
			return failure(status.Error(codes.PermissionDenied, "seed requires admin credential"))
		}
		db, e := platform.OpenDB(*dsnEnv)
		if e != nil {
			return failure(e)
		}
		defer db.Close()
		run, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		if e = tasks.Migrate(run, db, *migrations); e != nil {
			return failure(e)
		}
		if e = tasks.Seed(run, db, reg); e != nil {
			return failure(e)
		}
		kafka := bus.New(strings.Split(*brokers, ","))
		defer kafka.Close()
		if e = kafka.EnsureTopics(run, *topicPrefix); e != nil {
			return failure(e)
		}
		if e = WriteJSON(out, map[string]any{"seeded": true, "entities": len(reg.Bindings), "topics": 3}); e != nil {
			return failure(e)
		}
		return 0
	}
	conn, e := platform.Dial(*endpoint)
	if e != nil {
		return failure(e)
	}
	defer conn.Close()
	auth := platform.Outgoing(ctx, token)
	if command == "subscribe" {
		e = subscribe(auth, entityv1.NewEntityServiceClient(conn), subscriptionIDs, *duration, out, diagnostics)
		if e != nil {
			return failure(e)
		}
		return 0
	}
	rpc, cancel := context.WithTimeout(auth, 5*time.Second)
	defer cancel()
	var response proto.Message
	switch command {
	case "snapshot":
		response, e = entityv1.NewEntityServiceClient(conn).GetSnapshot(rpc, &entityv1.GetSnapshotRequest{EntityId: *entity})
	case "task create":
		payload, _ := json.Marshal(tasks.InspectParams{DurationSeconds: *seconds, Note: *note})
		req := &taskv1.CreateTaskRequest{IdempotencyKey: *key, TaskType: *kind, TargetEntityId: *entity, Payload: &commonv1.TaskPayload{PayloadJson: string(payload)}}
		req.Deadline = taskDeadline
		response, e = taskv1.NewTaskServiceClient(conn).CreateTask(rpc, req)
	case "task get":
		response, e = taskv1.NewTaskServiceClient(conn).GetTask(rpc, &taskv1.GetTaskRequest{TaskId: *id})
	case "task history":
		response, e = taskv1.NewTaskServiceClient(conn).GetTaskHistory(rpc, &taskv1.GetTaskHistoryRequest{TaskId: *id})
	case "task cancel":
		response, e = taskv1.NewTaskServiceClient(conn).CancelTask(rpc, &taskv1.CancelTaskRequest{TaskId: *id, Reason: *reason})
	case "task list":
		response, e = taskv1.NewTaskServiceClient(conn).ListTasks(rpc, &taskv1.ListTasksRequest{TargetEntityId: *entity, Status: st, PageSize: int32(*pageSize), PageToken: *pageToken})
	case "dispatch get":
		response, e = dispatcherv1.NewDispatcherServiceClient(conn).GetDispatch(rpc, &dispatcherv1.GetDispatchRequest{TaskId: *taskID, DispatchId: *dispatchID})
	case "dispatch retry-dlq":
		response, e = dispatcherv1.NewDispatcherServiceClient(conn).RetryDLQ(rpc, &dispatcherv1.RetryDLQRequest{TaskId: *taskID, Reason: *reason})
	case "dispatcher status":
		response, e = dispatcherv1.NewDispatcherServiceClient(conn).GetStatus(rpc, &dispatcherv1.GetStatusRequest{})
	}
	if e != nil {
		return failure(e)
	}
	if e = WriteProto(out, response); e != nil {
		return failure(e)
	}
	return 0
}

func subscribe(parent context.Context, client entityv1.EntityServiceClient, ids []string, duration time.Duration, out, diagnostics io.Writer) error {
	ctx, cancel := context.WithTimeout(parent, duration)
	defer cancel()
	view := map[string]*entityv1.EntityUpdate{}
	versions := map[string]*entityv1.EntityUpdate{}
	hasSnapshot := false
	reconnects := 0
	attempted := false
	for ctx.Err() == nil {
		if attempted {
			reconnects++
		}
		attempted = true
		stream, e := client.Subscribe(ctx, &entityv1.SubscribeRequest{EntityIds: ids})
		candidate := map[string]*entityv1.EntityUpdate{}
		initialized := false
		if e == nil {
			for {
				frame, recvErr := stream.Recv()
				if recvErr != nil {
					e = recvErr
					break
				}
				if e = WriteProto(out, frame); e != nil {
					return e
				}
				switch frame.Kind {
				case entityv1.EntityUpdateKind_ENTITY_UPDATE_KIND_SNAPSHOT:
					candidate[frame.EntityId] = frame
				case entityv1.EntityUpdateKind_ENTITY_UPDATE_KIND_SNAPSHOT_END:
					view = candidate
					versions = make(map[string]*entityv1.EntityUpdate, len(candidate))
					for id, value := range candidate {
						versions[id] = value
					}
					initialized = true
					hasSnapshot = true
				case entityv1.EntityUpdateKind_ENTITY_UPDATE_KIND_UPSERT, entityv1.EntityUpdateKind_ENTITY_UPDATE_KIND_DELETE:
					if !initialized {
						return errors.New("increment before SNAPSHOT_END")
					}
					old := versions[frame.EntityId]
					if old != nil && (old.SourceGeneration > frame.SourceGeneration || old.SourceGeneration == frame.SourceGeneration && old.Version >= frame.Version) {
						continue
					}
					// Keep delete versions until the next complete snapshot, so an
					// older upsert cannot resurrect a removed entity.
					versions[frame.EntityId] = frame
					if frame.Kind == entityv1.EntityUpdateKind_ENTITY_UPDATE_KIND_DELETE {
						delete(view, frame.EntityId)
					} else {
						view[frame.EntityId] = frame
					}
				}
			}
		}
		if ctx.Err() != nil {
			break
		}
		if !errors.Is(e, io.EOF) {
			switch status.Code(e) {
			case codes.Unavailable, codes.FailedPrecondition, codes.ResourceExhausted, codes.DeadlineExceeded:
			case codes.OK:
			default:
				return e
			}
		}
		if err := WriteJSON(diagnostics, map[string]any{"subscriptionReconnect": map[string]string{"reason": e.Error()}}); err != nil {
			return err
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	if parent.Err() != nil {
		return status.FromContextError(parent.Err()).Err()
	}
	if !hasSnapshot {
		return status.Error(codes.DeadlineExceeded, "subscription ended before a complete initial snapshot")
	}
	stale := 0
	for _, frame := range view {
		if frame.ExpiresAt != nil && !frame.ExpiresAt.AsTime().After(time.Now()) {
			stale++
		}
	}
	return WriteJSON(out, map[string]any{"subscriptionSummary": map[string]int{"entityCount": len(view), "staleCount": stale, "reconnects": reconnects}})
}
