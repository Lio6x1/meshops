package verification

import (
	"context"
	"encoding/json"
	"errors"
	commonv1 "example.com/meshops-course/gen/common/v1"
	entityv1 "example.com/meshops-course/gen/entity/v1"
	ingestv1 "example.com/meshops-course/gen/ingest/v1"
	taskv1 "example.com/meshops-course/gen/task/v1"
	"example.com/meshops-course/internal/platform"
	"example.com/meshops-course/internal/state"
	"fmt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

type FaultStep struct {
	Dependency, StoppedAt, RestoredAt, FailureObserved string
	FailureSeconds, RecoverySeconds                    float64
	Recovered                                          bool
}
type FaultReport struct {
	RunID, StartedAt, EvidenceDirectory, Database, TopicPrefix string
	Steps                                                      []FaultStep
	Passed                                                     bool
	Failure                                                    string `json:"failure,omitempty"`
}

func recoverDependency(stop, restore func() error, work func(func() error) error) (err error) {
	restored := false
	recoverNow := func() error {
		err := restore()
		if err == nil {
			restored = true
		}
		return err
	}
	defer func() {
		if !restored {
			err = errors.Join(err, recoverNow())
		}
	}()
	// A failed stop can mean the dependency stopped but its response was lost.
	// Install recovery first, and preserve both primary and restoration errors.
	if err = stop(); err != nil {
		return err
	}
	return work(recoverNow)
}

func (e *Environment) compose(ctx context.Context, args ...string) error {
	command := exec.CommandContext(ctx, "docker", append([]string{"compose", "-f", filepath.Join(e.Root, "docker-compose.yml")}, args...)...)
	hide(command)
	command.Dir = e.Root
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("compose %v: %w: %s", args, err, output)
	}
	return nil
}
func (e *Environment) publishVersion(ctx context.Context, version int64) error {
	source := e.Sources[0]
	now := time.Now().UTC()
	raw, err := state.GenerateRaw(source, "person-00000", version, now)
	if err != nil {
		return err
	}
	event, err := state.Normalize(raw, source, now)
	if err != nil {
		return err
	}
	conn, err := platform.Dial(e.Processes["ingest"].Endpoint)
	if err != nil {
		return err
	}
	defer conn.Close()
	token, err := e.Registry.Credential(e.Tenant, source.ID)
	if err != nil {
		return err
	}
	c, stop := context.WithTimeout(platform.Outgoing(ctx, token), 8*time.Second)
	defer stop()
	stream, err := ingestv1.NewIngestServiceClient(conn).ReportEntityStates(c)
	if err != nil {
		return err
	}
	epoch := platform.NewID()
	if err = stream.Send(&ingestv1.ReportEntityStatesRequest{GatewayEpoch: epoch, FirstSequence: 1, Events: []*commonv1.EntityStateEvent{event}}); err != nil {
		return err
	}
	response, err := stream.Recv()
	if err != nil {
		return err
	}
	if response.GatewayEpoch != epoch || response.ConfirmedSequence != 1 || len(response.Errors) != 0 {
		return errors.New("kafka publication not confirmed")
	}
	return stream.CloseSend()
}
func (e *Environment) waitVersion(ctx context.Context, version int64) error {
	limit := time.NewTimer(60 * time.Second)
	defer limit.Stop()
	for {
		c, stop := context.WithTimeout(platform.Outgoing(ctx, e.Operator), 3*time.Second)
		snapshot, err := e.Entity.GetSnapshot(c, &entityv1.GetSnapshotRequest{EntityId: "person-00000"})
		stop()
		if err == nil && snapshot.Found && snapshot.Version >= version {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-limit.C:
			return fmt.Errorf("version %d not recovered", version)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// Faults intentionally stops the three dedicated reference Compose services.
// Run serially, with no demo or integration suite using this Compose project.
func Faults(ctx context.Context, root string) (report FaultReport, err error) {
	env, err := NewEnvironment(ctx, root, 1, 1)
	if err != nil {
		return report, err
	}
	defer env.Close()
	report = FaultReport{RunID: env.ID, StartedAt: time.Now().UTC().Format(time.RFC3339), EvidenceDirectory: env.Dir, Database: env.DBName, TopicPrefix: env.Prefix}
	defer func() {
		if err != nil {
			report.Failure = err.Error()
		}
		raw, x := json.MarshalIndent(report, "", "  ")
		if x == nil {
			x = os.WriteFile(filepath.Join(env.Dir, "faults.json"), raw, 0600)
		}
		if err == nil {
			err = x
		}
	}()
	for _, role := range []string{"entity", "task", "dispatcher", "ingest"} {
		if err = env.Start(ctx, role); err != nil {
			return report, err
		}
	}
	if err = env.Connect(); err != nil {
		return report, err
	}
	if err = env.publishVersion(ctx, 1); err != nil {
		return report, err
	}
	if err = env.waitVersion(ctx, 1); err != nil {
		return report, err
	}
	conn, err := platform.Dial(env.Processes["task"].Endpoint)
	if err != nil {
		return report, err
	}
	defer conn.Close()
	tasks := taskv1.NewTaskServiceClient(conn)
	c, stop := context.WithTimeout(platform.Outgoing(ctx, env.Operator), 8*time.Second)
	created, err := tasks.CreateTask(c, &taskv1.CreateTaskRequest{IdempotencyKey: "fault-probe", TargetEntityId: "person-00000", TaskType: "inspect", Payload: &commonv1.TaskPayload{PayloadJson: `{"duration_seconds":1}`}})
	stop()
	if err != nil {
		return report, err
	}
	version := int64(1)
	for _, dependency := range []string{"kafka", "redis", "mysql", "entity-process"} {
		step := FaultStep{Dependency: dependency}
		err = func() error {
			stopDependency := func() error {
				if dependency == "entity-process" {
					env.Stop("entity")
					return nil
				}
				c, stop := context.WithTimeout(ctx, 35*time.Second)
				x := env.compose(c, "stop", "-t", "10", dependency)
				stop()
				return x
			}
			restore := func() error {
				c, stop := context.WithTimeout(context.Background(), 150*time.Second)
				defer stop()
				if dependency == "entity-process" {
					return env.Start(c, "entity")
				}
				// Restart the exact container stopped above. Base-only `up` can
				// recreate an instance configured by an overlay (for example the
				// Search MySQL binlog flags), changing the environment under test.
				return env.compose(c, "start", "--wait", dependency)
			}
			return recoverDependency(stopDependency, restore, func(restore func() error) error {
				step.StoppedAt = time.Now().UTC().Format(time.RFC3339Nano)
				began := time.Now()
				var observed error
				switch dependency {
				case "kafka":
					version++
					observed = env.publishVersion(ctx, version)
					if observed == nil {
						return errors.New("kafka outage falsely confirmed publication")
					}
				case "mysql":
					c, stop := context.WithTimeout(platform.Outgoing(ctx, env.Operator), 8*time.Second)
					_, observed = tasks.GetTask(c, &taskv1.GetTaskRequest{TaskId: created.TaskId})
					stop()
					if status.Code(observed) != codes.Unavailable {
						return fmt.Errorf("MySQL outage should be UNAVAILABLE: %v", observed)
					}
				default:
					c, stop := context.WithTimeout(platform.Outgoing(ctx, env.Operator), 8*time.Second)
					_, observed = env.Entity.GetSnapshot(c, &entityv1.GetSnapshotRequest{EntityId: "person-00000"})
					stop()
					if status.Code(observed) != codes.Unavailable {
						return fmt.Errorf("%s outage should be UNAVAILABLE: %v", dependency, observed)
					}
					version++
					if x := env.publishVersion(ctx, version); x != nil {
						return fmt.Errorf("healthy Ingest/Kafka failed to retain outage event: %w", x)
					}
					lag, x := env.Bus.Lag(ctx, env.projectorGroup, env.Prefix+"entity-state-events.v1")
					if x != nil || lag < 1 {
						return fmt.Errorf("failed projection did not retain lag: %d %v", lag, x)
					}
				}
				step.FailureSeconds = time.Since(began).Seconds()
				step.FailureObserved = observed.Error()
				began = time.Now()
				if x := restore(); x != nil {
					return x
				}
				step.RestoredAt = time.Now().UTC().Format(time.RFC3339Nano)
				if dependency == "kafka" {
					if x := env.publishVersion(ctx, version); x != nil {
						return x
					}
				}
				if dependency == "mysql" {
					c, stop := context.WithTimeout(platform.Outgoing(ctx, env.Operator), 8*time.Second)
					r, x := tasks.GetTask(c, &taskv1.GetTaskRequest{TaskId: created.TaskId})
					stop()
					if x != nil || r.GetTask().GetTaskId() != created.TaskId {
						return fmt.Errorf("durable task not recovered: %v", x)
					}
				} else {
					if x := env.waitVersion(ctx, version); x != nil {
						return x
					}
				}
				step.RecoverySeconds = time.Since(began).Seconds()
				step.Recovered = true
				return nil
			})
		}()
		report.Steps = append(report.Steps, step)
		if err != nil {
			return report, err
		}
	}
	report.Passed = true
	return report, nil
}
