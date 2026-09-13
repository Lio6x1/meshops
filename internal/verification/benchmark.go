package verification

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	commonv1 "example.com/meshops-course/gen/common/v1"
	entityv1 "example.com/meshops-course/gen/entity/v1"
	ingestv1 "example.com/meshops-course/gen/ingest/v1"
	"example.com/meshops-course/internal/platform"
	"example.com/meshops-course/internal/state"
	"google.golang.org/protobuf/proto"
)

type Metrics struct {
	Series     int     `json:"series"`
	HeapBytes  float64 `json:"goHeapBytes"`
	Goroutines float64 `json:"goroutines"`
}

func metrics(addr string) (Metrics, error) {
	c := &http.Client{Timeout: 3 * time.Second}
	r, err := c.Get("http://" + addr + "/metrics")
	if err != nil {
		return Metrics{}, err
	}
	defer r.Body.Close()
	if r.StatusCode != 200 {
		return Metrics{}, fmt.Errorf("metrics status %d", r.StatusCode)
	}
	var m Metrics
	s := bufio.NewScanner(r.Body)
	for s.Scan() {
		line := s.Text()
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		m.Series++
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		v, _ := strconv.ParseFloat(fields[1], 64)
		switch fields[0] {
		case "go_memstats_heap_alloc_bytes":
			m.HeapBytes = v
		case "go_goroutines":
			m.Goroutines = v
		}
	}
	return m, s.Err()
}

type Latency struct {
	Samples int     `json:"samples"`
	P50     float64 `json:"p50Ms"`
	P99     float64 `json:"p99Ms"`
	Max     float64 `json:"maxMs"`
}
type ByteDistribution struct {
	Samples int     `json:"samples"`
	P50     float64 `json:"p50Bytes"`
	P99     float64 `json:"p99Bytes"`
	Max     float64 `json:"maxBytes"`
}

func distribution(values []float64) Latency {
	if len(values) == 0 {
		return Latency{}
	}
	sort.Float64s(values)
	at := func(p float64) float64 { i := int(float64(len(values)-1) * p); return values[i] }
	return Latency{len(values), at(.5), at(.99), values[len(values)-1]}
}

type Phase struct {
	ConsumerDrainElapsed  float64          `json:"consumerDrainSeconds"`
	Requested             int              `json:"requested"`
	GenerationElapsed     float64          `json:"generationSeconds"`
	GeneratedRate         float64          `json:"generatedEventsPerSecond"`
	OfferedRate           int              `json:"offeredEventsPerSecond"`
	Scheduled             int              `json:"scheduled"`
	QueueDrops            int              `json:"generatorQueueDrops"`
	Accepted              int              `json:"accepted"`
	AcceptedByType        map[string]int   `json:"acceptedByType"`
	Errors                int              `json:"errors"`
	ErrorExamples         []string         `json:"errorExamples"`
	Elapsed               float64          `json:"elapsedIncludingDrainSeconds"`
	Throughput            float64          `json:"acceptedEventsPerSecond"`
	ACK                   Latency          `json:"clientToKafkaAck"`
	Visible               Latency          `json:"generatedToObserved"`
	ObservationCandidates int              `json:"observationCandidates"`
	ObservationMisses     int              `json:"observationMisses"`
	ProjectorLag          int64            `json:"projectorLag"`
	HistoryLag            int64            `json:"historyLag"`
	Ingest                Metrics          `json:"ingest"`
	Entity                Metrics          `json:"entity"`
	MessageBytes          ByteDistribution `json:"messageBytesDistribution"`
}
type BenchmarkReport struct {
	SourceStaleAfterSeconds                           int              `json:"sourceStaleAfterSeconds"`
	WarmupExpiredSnapshots                            int              `json:"warmupExpiredSnapshots"`
	WarmupSnapshotMeaning                             string           `json:"warmupSnapshotMeaning"`
	Stage                                             string           `json:"stage"`
	Requested                                         BenchmarkOptions `json:"requested"`
	Warmup                                            Phase            `json:"warmup"`
	RunID, StartedAt, Go, OS, Arch, CPU               string
	LogicalCPUs, Entities, Sources, Partitions, Batch int
	Scope, MemoryMeaning, ObservationMeaning          string
	InitialIngest, InitialEntity                      Metrics
	WarmupAccepted                                    int
	WarmupSnapshots                                   int
	Profile                                           string         `json:"profile"`
	EntityCountsByType                                map[string]int `json:"entityCountsByType"`
	SourceCountsByType                                map[string]int `json:"sourceCountsByType"`
	WarmupAcceptedByType                              map[string]int `json:"warmupAcceptedByType"`
	WarmupSnapshotsByType                             map[string]int `json:"warmupSnapshotsByType"`
	Failure                                           string         `json:"failure,omitempty"`
	Phases                                            []Phase
	EvidenceDirectory, Database, TopicPrefix          string
}
type loadJob struct {
	event   *commonv1.EntityStateEvent
	source  int
	created time.Time
	observe bool
}
type loadDriver struct {
	generators    []*state.RawGenerator
	environment   *Environment
	versions      []int64
	cursor        int64
	targets       []loadTarget
	warmupTimeout time.Duration
}

func (d *loadDriver) job(observe bool) (loadJob, error) {
	index := int(d.cursor % int64(len(d.targets)))
	d.cursor++
	target := d.targets[index]
	d.versions[index]++
	now := time.Now().UTC()
	source := d.environment.Sources[target.source]
	raw, err := d.generators[target.source].Generate(target.rawID, d.versions[index], now)
	if err != nil {
		return loadJob{}, err
	}
	event, err := state.Normalize(raw, source, now)
	return loadJob{event, target.source, now, observe}, err
}
func (d *loadDriver) phase(ctx context.Context, rate, count int, warmup bool) (Phase, error) {
	e := d.environment
	budget := phaseBudget(rate, count, warmup)
	if warmup && d.warmupTimeout > 0 {
		budget = d.warmupTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	start := time.Now()
	result := Phase{Requested: count, OfferedRate: rate, AcceptedByType: map[string]int{}}
	var mu sync.Mutex
	var ack, visible, sizes []float64
	var workers, observers sync.WaitGroup
	observations := make(chan loadJob, 2048)
	queues := make([]chan loadJob, len(e.Sources))
	recordError := func(err error) {
		mu.Lock()
		defer mu.Unlock()
		result.Errors++
		if len(result.ErrorExamples) < 5 {
			result.ErrorExamples = append(result.ErrorExamples, err.Error())
		}
	}
	for i := 0; i < 4; i++ {
		observers.Add(1)
		go func() {
			defer observers.Done()
			for job := range observations {
				deadline := time.Now().Add(20 * time.Second)
				observed := false
				for time.Now().Before(deadline) && ctx.Err() == nil {
					rpc, stop := context.WithTimeout(platform.Outgoing(ctx, e.Operator), 2*time.Second)
					snapshot, err := e.Entity.GetSnapshot(rpc, &entityv1.GetSnapshotRequest{EntityId: job.event.EntityId})
					stop()
					if err == nil && snapshot.Found && snapshot.SourceGeneration == job.event.SourceGeneration && snapshot.Version >= job.event.EntityVersion {
						mu.Lock()
						visible = append(visible, float64(time.Since(job.created).Microseconds())/1000)
						mu.Unlock()
						observed = true
						break
					}
					select {
					case <-ctx.Done():
					case <-time.After(5 * time.Millisecond):
					}
				}
				if !observed {
					mu.Lock()
					result.ObservationMisses++
					mu.Unlock()
				}
			}
		}()
	}
	for index, source := range e.Sources {
		queues[index] = make(chan loadJob, 128)
		workers.Add(1)
		go func(index int, source platform.Source) {
			defer workers.Done()
			conn, err := platform.Dial(e.Processes["ingest"].Endpoint)
			if err != nil {
				recordError(err)
				for range queues[index] {
				}
				return
			}
			defer conn.Close()
			token, _ := e.Registry.Credential(e.Tenant, source.ID)
			client := ingestv1.NewIngestServiceClient(conn)
			var stream ingestv1.IngestService_ReportEntityStatesClient
			var streamCancel context.CancelFunc
			var confirmed, resume int64
			epoch := platform.NewID()
			defer func() {
				if streamCancel != nil {
					streamCancel()
				}
			}()
			for job := range queues[index] {
				if stream == nil {
					streamCtx, stop := context.WithCancel(platform.Outgoing(ctx, token))
					streamCancel = stop
					stream, err = client.ReportEntityStates(streamCtx)
					resume = confirmed
					if err != nil {
						stop()
						recordError(err)
						continue
					}
				}
				began := time.Now()
				request := &ingestv1.ReportEntityStatesRequest{GatewayEpoch: epoch, FirstSequence: confirmed + 1, ResumeAfterSequence: resume, Events: []*commonv1.EntityStateEvent{job.event}}
				// 每个来源只有一条流和一个在途批次。超时会取消该传输，
				// 不会让 goroutine 因失效的 broker 而无限阻塞。
				timeout := time.AfterFunc(10*time.Second, streamCancel)
				err = stream.Send(request)
				var response *ingestv1.ReportEntityStatesResponse
				if err == nil {
					response, err = stream.Recv()
				}
				timeout.Stop()
				if err == nil && (response.GatewayEpoch != epoch || response.ConfirmedSequence != confirmed+1 || len(response.Errors) != 0) {
					err = errors.New("publication was not fully confirmed")
				}
				if err != nil {
					recordError(err)
					streamCancel()
					stream = nil
					continue
				}
				confirmed = response.ConfirmedSequence
				mu.Lock()
				result.Accepted++
				result.AcceptedByType[source.Adapter]++
				ack = boundedSample(ack, float64(time.Since(began).Microseconds())/1000, result.Accepted)
				sizes = boundedSample(sizes, float64(proto.Size(request)), result.Accepted)
				if job.observe {
					result.ObservationCandidates++
				}
				mu.Unlock()
				if job.observe {
					select {
					case observations <- job:
					case <-ctx.Done():
						mu.Lock()
						result.ObservationMisses++
						mu.Unlock()
					}
				}
			}
			if stream != nil {
				_ = stream.CloseSend()
			}
		}(index, source)
	}
	// 以绝对时间安排总事件数量，避免高频 ticker 合并通知。
	generationStart := time.Now()
	nextProgress := time.Now().Add(5 * time.Second)
	var generationErr error
	for i := 0; i < count && ctx.Err() == nil; i++ {
		if !warmup {
			due := generationStart.Add(time.Duration(i+1) * time.Second / time.Duration(rate))
			if delay := time.Until(due); delay > 0 {
				timer := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					timer.Stop()
				case <-timer.C:
				}
			}
			if ctx.Err() != nil {
				break
			}
		}
		if time.Now().After(nextProgress) {
			progress("generate", i, count)
			nextProgress = time.Now().Add(5 * time.Second)
		}
		job, err := d.job(!warmup && observeScheduled(i, len(e.Sources)))
		if err != nil {
			generationErr = err
			break
		}
		result.Scheduled++
		if warmup {
			select {
			case queues[job.source] <- job:
			case <-ctx.Done():
			}
		} else {
			select {
			case queues[job.source] <- job:
			default:
				result.QueueDrops++
			}
		}
	}
	result.GenerationElapsed = time.Since(generationStart).Seconds()
	if result.GenerationElapsed > 0 {
		result.GeneratedRate = float64(result.Scheduled) / result.GenerationElapsed
	}
	for _, q := range queues {
		close(q)
	}
	workers.Wait()
	close(observations)
	observers.Wait()
	updateElapsed := func() {
		result.Elapsed = time.Since(start).Seconds()
		result.Throughput = float64(result.Accepted) / result.Elapsed
	}
	updateElapsed()
	result.ACK = distribution(ack)
	result.Visible = distribution(visible)
	result.MessageBytes = ByteDistribution(distribution(sizes))
	if generationErr != nil {
		return result, generationErr
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	var probeErr error
	progress("consumer_drain", result.Accepted, count)
	drainStart := time.Now()
	result.ProjectorLag, result.HistoryLag, probeErr = drainConsumers(ctx, 30*time.Second, func(probeCtx context.Context) (int64, int64, error) {
		projector, err := e.Bus.Lag(probeCtx, e.projectorGroup, e.Prefix+"entity-state-events.v1")
		if err != nil {
			return 0, 0, err
		}
		history, err := e.Bus.Lag(probeCtx, e.Prefix+"entity-history-v1", e.Prefix+"entity-state-events.v1")
		return projector, history, err
	})
	result.ConsumerDrainElapsed = time.Since(drainStart).Seconds()
	// 有限排空仍是本档处理成本，不能从吞吐分母中剔除。
	updateElapsed()
	if probeErr != nil {
		return result, probeErr
	}
	result.Ingest, probeErr = metrics(e.Processes["ingest"].Metrics)
	if probeErr != nil {
		return result, probeErr
	}
	result.Entity, probeErr = metrics(e.Processes["entity"].Metrics)
	if probeErr != nil {
		return result, probeErr
	}
	return result, nil
}

func phaseBudget(rate, count int, warmup bool) time.Duration {
	budget := 3 * time.Minute
	if !warmup && rate > 0 {
		budget += time.Duration(count) * time.Second / time.Duration(rate)
	}
	return budget
}
func Benchmark(ctx context.Context, root string, seconds int) (BenchmarkReport, error) {
	return BenchmarkWithProfile(ctx, root, seconds, "mixed")
}
func BenchmarkWithProfile(ctx context.Context, root string, seconds int, profile string) (BenchmarkReport, error) {
	o := DefaultBenchmarkOptions()
	o.Seconds = seconds
	o.Profile = profile
	return BenchmarkWithOptions(ctx, root, o)
}

// 报告先于初始化创建；依赖和进程启动失败仍留下请求与失败阶段。
func BenchmarkWithOptions(ctx context.Context, root string, o BenchmarkOptions) (report BenchmarkReport, err error) {
	id := strings.ReplaceAll(platform.NewID(), "-", "")
	report = BenchmarkReport{RunID: id, StartedAt: time.Now().UTC().Format(time.RFC3339), Go: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH, CPU: os.Getenv("PROCESSOR_IDENTIFIER"), LogicalCPUs: runtime.NumCPU(), Entities: o.Entities, Sources: 10, Partitions: 3, Batch: 1, Profile: o.Profile, Requested: o, Stage: "validate", EvidenceDirectory: filepath.Join(root, ".local", "verification", id), Scope: "real authenticated Ingest -> Kafka -> Entity -> Redis/MySQL; excludes gateway bbolt, tasks and subscription fanout", MemoryMeaning: "Go live heap; container RSS and limits are recorded by scale runner", ObservationMeaning: "rotating sample across ten sources; polling same or higher version; ACK/message reservoirs at most 100000; every warmup identity verified", EntityCountsByType: map[string]int{}, SourceCountsByType: map[string]int{}, WarmupSnapshotsByType: map[string]int{}}
	if err = os.MkdirAll(report.EvidenceDirectory, 0700); err != nil {
		report.Failure = err.Error()
		return report, err
	}
	defer func() {
		if err != nil {
			report.Failure = err.Error()
		}
		out, writeErr := json.MarshalIndent(report, "", "  ")
		if writeErr == nil {
			writeErr = os.WriteFile(filepath.Join(report.EvidenceDirectory, "benchmark.json"), out, 0600)
		}
		if writeErr != nil {
			err = errors.Join(err, writeErr)
			report.Failure = err.Error()
		}
	}()
	if err = o.Validate(); err != nil {
		return report, err
	}
	report.Stage = "initialize"
	report.SourceStaleAfterSeconds = 30
	report.WarmupSnapshotMeaning = "full retained snapshot identity, source, type, generation and version; expired snapshots remain readable and are counted separately; not a freshness guarantee"
	initial, writeErr := json.MarshalIndent(report, "", "  ")
	if writeErr != nil {
		return report, writeErr
	}
	if err = os.WriteFile(filepath.Join(report.EvidenceDirectory, "benchmark.json"), initial, 0600); err != nil {
		return report, err
	}
	progress(report.Stage, 0, o.Entities)
	env, err := newEnvironmentAt(ctx, root, o.Entities, 10, o.Profile, id)
	if err != nil {
		return report, err
	}
	defer env.Close()
	report.Database = env.DBName
	report.TopicPrefix = env.Prefix
	for _, source := range env.Sources {
		report.EntityCountsByType[source.Adapter] += len(source.Entities)
		report.SourceCountsByType[source.Adapter]++
	}
	for _, role := range []string{"entity", "ingest"} {
		report.Stage = "start_" + role
		progress(report.Stage, 0, o.Entities)
		if err = env.Start(ctx, role); err != nil {
			return report, err
		}
	}
	if err = env.Connect(); err != nil {
		return report, err
	}
	stopDiagnostics, err := startDiagnostics(ctx, env)
	if err != nil {
		return report, err
	}
	defer func() {
		if diagnosticErr := stopDiagnostics(); diagnosticErr != nil {
			err = errors.Join(err, diagnosticErr)
		}
	}()
	report.InitialIngest, err = metrics(env.Processes["ingest"].Metrics)
	if err != nil {
		return report, err
	}
	report.InitialEntity, err = metrics(env.Processes["entity"].Metrics)
	if err != nil {
		return report, err
	}
	driver, err := newLoadDriver(env)
	if err != nil {
		return report, err
	}
	driver.warmupTimeout = o.WarmupTimeout
	report.Stage = "warmup_publish"
	progress(report.Stage, 0, o.Entities)
	warmCtx, stop := context.WithTimeout(ctx, o.WarmupTimeout)
	defer stop()
	report.Warmup, err = driver.phase(warmCtx, 0, o.Entities, true)
	report.WarmupAccepted = report.Warmup.Accepted
	report.WarmupAcceptedByType = report.Warmup.AcceptedByType
	if err != nil {
		return report, err
	}
	if report.WarmupAccepted != o.Entities || report.Warmup.Errors != 0 {
		return report, fmt.Errorf("warmup accepted %d/%d; %v", report.WarmupAccepted, o.Entities, report.Warmup.ErrorExamples)
	}
	// phase 已在剩余预热预算内排空投影与历史消费组，再逐一检查快照。
	report.Stage = "warmup_snapshots"
	err = verifyWarmup(warmCtx, driver, &report, func(c context.Context, ids []string) (*entityv1.BatchGetSnapshotsResponse, error) {
		return env.Entity.BatchGetSnapshots(c, &entityv1.BatchGetSnapshotsRequest{EntityIds: ids})
	})
	if err != nil {
		return report, err
	}
	for _, rate := range o.Rates {
		report.Stage = fmt.Sprintf("load_%d", rate)
		progress(report.Stage, 0, rate*o.Seconds)
		phase, x := driver.phase(ctx, rate, rate*o.Seconds, false)
		report.Phases = append(report.Phases, phase)
		if x != nil {
			return report, x
		}
		if err = phaseFailure(phase); err != nil {
			return report, err
		}
	}
	report.Stage = "complete"
	progress(report.Stage, o.Entities, o.Entities)
	return report, nil
}

func progress(stage string, done, total int) {
	fmt.Fprintf(os.Stderr, "%s stage=%s completed=%d total=%d\n", time.Now().UTC().Format(time.RFC3339), stage, done, total)
}

// 大流量运行只保留有界且无偏的 ACK/消息大小样本。
func boundedSample(values []float64, value float64, seen int) []float64 {
	const limit = 100000
	if len(values) < limit {
		return append(values, value)
	}
	if index := rand.Intn(seen); index < limit {
		values[index] = value
	}
	return values
}
