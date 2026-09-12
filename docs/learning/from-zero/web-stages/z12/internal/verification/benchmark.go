package verification

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	OfferedRate           int              `json:"offeredEventsPerSecond"`
	Scheduled             int              `json:"scheduled"`
	QueueDrops            int              `json:"generatorQueueDrops"`
	Accepted              int              `json:"accepted"`
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
	RunID, StartedAt, Go, OS, Arch, CPU               string
	LogicalCPUs, Entities, Sources, Partitions, Batch int
	Scope, MemoryMeaning, ObservationMeaning          string
	InitialIngest, InitialEntity                      Metrics
	WarmupAccepted                                    int
	WarmupSnapshots                                   int
	Failure                                           string `json:"failure,omitempty"`
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
	environment *Environment
	versions    []int64
	cursor      int64
	count       int
}

func (d *loadDriver) job(observe bool) (loadJob, error) {
	n := int(d.cursor % int64(d.count))
	d.cursor++
	sources := len(d.environment.Sources)
	s := n % sources
	index := s*(d.count/sources) + n/sources
	d.versions[index]++
	id := fmt.Sprintf("person-%05d", index)
	now := time.Now().UTC()
	source := d.environment.Sources[s]
	raw, err := state.GenerateRaw(source, id, d.versions[index], now)
	if err != nil {
		return loadJob{}, err
	}
	event, err := state.Normalize(raw, source, now)
	return loadJob{event, s, now, observe}, err
}
func (d *loadDriver) phase(ctx context.Context, rate, count int, warmup bool) (Phase, error) {
	e := d.environment
	ctx, cancel := context.WithTimeout(ctx, phaseBudget(rate, count, warmup))
	defer cancel()
	start := time.Now()
	result := Phase{OfferedRate: rate}
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
				ack = append(ack, float64(time.Since(began).Microseconds())/1000)
				sizes = append(sizes, float64(proto.Size(request)))
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
	var tick *time.Ticker
	if !warmup {
		tick = time.NewTicker(time.Second / time.Duration(rate))
		defer tick.Stop()
	}
	var generationErr error
	for i := 0; i < count && ctx.Err() == nil; i++ {
		if tick != nil {
			select {
			case <-ctx.Done():
				break
			case <-tick.C:
			}
		}
		job, err := d.job(!warmup && i%50 == 0)
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
	for _, q := range queues {
		close(q)
	}
	workers.Wait()
	result.Elapsed = time.Since(start).Seconds()
	close(observations)
	observers.Wait()
	if generationErr != nil {
		return result, generationErr
	}
	if ctx.Err() != nil {
		return result, ctx.Err()
	}
	result.Throughput = float64(result.Accepted) / result.Elapsed
	result.ACK = distribution(ack)
	result.Visible = distribution(visible)
	byteStats := distribution(sizes)
	result.MessageBytes = ByteDistribution(byteStats)
	var probeErr error
	result.ProjectorLag, probeErr = e.Bus.Lag(ctx, e.projectorGroup, e.Prefix+"entity-state-events.v1")
	if probeErr != nil {
		return result, probeErr
	}
	result.HistoryLag, probeErr = e.Bus.Lag(ctx, e.Prefix+"entity-history-v1", e.Prefix+"entity-state-events.v1")
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
func Benchmark(ctx context.Context, root string, seconds int) (report BenchmarkReport, err error) {
	if seconds < 5 || seconds > 300 {
		return report, errors.New("duration must be 5..300 seconds")
	}
	env, err := NewEnvironment(ctx, root, 10000, 10)
	if err != nil {
		return report, err
	}
	defer env.Close()
	report = BenchmarkReport{RunID: env.ID, StartedAt: time.Now().UTC().Format(time.RFC3339), Go: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH, CPU: os.Getenv("PROCESSOR_IDENTIFIER"), LogicalCPUs: runtime.NumCPU(), Entities: 10000, Sources: 10, Partitions: 3, Batch: 1, Scope: "real standalone Ingest -> Kafka -> Entity -> Redis/MySQL; excludes gateway bbolt, task execution and subscription fanout", MemoryMeaning: "Go live heap per standalone service; not RSS or total machine RAM", ObservationMeaning: "every 50th scheduled event; independent polling observes same or higher entity version; latency includes polling delay", EvidenceDirectory: env.Dir, Database: env.DBName, TopicPrefix: env.Prefix}
	defer func() {
		if err != nil {
			report.Failure = err.Error()
		}
		out, writeErr := json.MarshalIndent(report, "", "  ")
		if writeErr == nil {
			writeErr = os.WriteFile(filepath.Join(env.Dir, "benchmark.json"), out, 0600)
		}
		if err == nil {
			err = writeErr
		}
	}()
	for _, role := range []string{"entity", "ingest"} {
		if err = env.Start(ctx, role); err != nil {
			return report, err
		}
	}
	if err = env.Connect(); err != nil {
		return report, err
	}
	report.InitialIngest, err = metrics(env.Processes["ingest"].Metrics)
	if err != nil {
		return report, err
	}
	report.InitialEntity, err = metrics(env.Processes["entity"].Metrics)
	if err != nil {
		return report, err
	}
	driver := &loadDriver{environment: env, versions: make([]int64, 10000), count: 10000}
	warmup, err := driver.phase(ctx, 0, 10000, true)
	if err != nil {
		return report, err
	}
	report.WarmupAccepted = warmup.Accepted
	if warmup.Accepted != 10000 {
		return report, fmt.Errorf("warmup accepted %d/10000; %v", warmup.Accepted, warmup.ErrorExamples)
	}
	// 等待所有预热事件应用完成后，再测量稳定负载。
	limit := time.Now().Add(60 * time.Second)
	for {
		lag, x := env.Bus.Lag(ctx, env.projectorGroup, env.Prefix+"entity-state-events.v1")
		if x != nil {
			return report, x
		}
		if lag == 0 {
			break
		}
		if time.Now().After(limit) {
			return report, fmt.Errorf("warmup projection lag remains %d", lag)
		}
		time.Sleep(100 * time.Millisecond)
	}
	// 仅凭 broker ACK 和零积压，无法证明每个不同实体都已投影，
	// 因为格式错误的记录可能已被隔离。
	for start := 0; start < 10000; start += 100 {
		ids := make([]string, 100)
		for i := range ids {
			ids[i] = fmt.Sprintf("person-%05d", start+i)
		}
		c, stop := context.WithTimeout(platform.Outgoing(ctx, env.Operator), 5*time.Second)
		response, x := env.Entity.BatchGetSnapshots(c, &entityv1.BatchGetSnapshotsRequest{EntityIds: ids})
		stop()
		if x != nil {
			return report, x
		}
		if len(response.Snapshots) != len(ids) {
			return report, errors.New("warmup batch omitted entities")
		}
		for i, snapshot := range response.Snapshots {
			if !snapshot.Found || snapshot.EntityId != ids[i] || snapshot.SourceGeneration != 1 || snapshot.Version != 1 {
				return report, fmt.Errorf("warmup snapshot mismatch %s", ids[i])
			}
			report.WarmupSnapshots++
		}
	}
	for _, rate := range []int{100, 500} {
		phase, x := driver.phase(ctx, rate, rate*seconds, false)
		report.Phases = append(report.Phases, phase)
		if x != nil {
			return report, x
		}
	}
	return report, nil
}
