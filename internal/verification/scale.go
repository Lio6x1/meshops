package verification

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	entityv1 "example.com/meshops-course/gen/entity/v1"
	"example.com/meshops-course/internal/platform"
)

// 实体基数与事件速率独立；每个来源拥有相同数量的实体。
type BenchmarkOptions struct {
	Entities      int           `json:"entities"`
	Rates         []int         `json:"rates"`
	Seconds       int           `json:"seconds"`
	Profile       string        `json:"profile"`
	WarmupTimeout time.Duration `json:"warmupTimeoutNanoseconds"`
}

func DefaultBenchmarkOptions() BenchmarkOptions {
	return BenchmarkOptions{Entities: 10000, Rates: []int{100, 500}, Seconds: 30, Profile: "mixed", WarmupTimeout: 10 * time.Minute}
}

func (o BenchmarkOptions) Validate() error {
	if o.Entities < 10 || o.Entities > 1000000 || o.Entities%10 != 0 {
		return errors.New("entities must be 10..1000000 and divisible by 10")
	}
	if o.Seconds < 5 || o.Seconds > 300 {
		return errors.New("duration must be 5..300 seconds")
	}
	if o.WarmupTimeout < time.Second || o.WarmupTimeout > 2*time.Hour {
		return errors.New("warmup timeout must be 1s..2h")
	}
	if len(o.Rates) == 0 || len(o.Rates) > 10 {
		return errors.New("rates requires 1..10 offered loads")
	}
	seen := map[int]bool{}
	for _, rate := range o.Rates {
		if rate < 1 || rate > 100000 || seen[rate] {
			return errors.New("rates must be distinct values in 1..100000")
		}
		seen[rate] = true
	}
	return ValidateBenchmarkProfile(o.Profile)
}

func ParseBenchmarkRates(raw string) ([]int, error) {
	var rates []int
	for _, part := range strings.Split(raw, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil {
			return nil, errors.New("rates must be comma-separated integers")
		}
		rates = append(rates, n)
	}
	return rates, nil
}

type snapshotBatch func(context.Context, []string) (*entityv1.BatchGetSnapshotsResponse, error)

// 所有身份逐一核验，最后不足 100 条的尾批同样检查。
func verifyWarmup(ctx context.Context, d *loadDriver, report *BenchmarkReport, fetch snapshotBatch) error {
	for start := 0; start < len(d.targets); start += 100 {
		end := min(start+100, len(d.targets))
		ids := make([]string, end-start)
		for i := range ids {
			ids[i] = d.targets[start+i].entityID
		}
		c, stop := context.WithTimeout(platform.Outgoing(ctx, d.environment.Operator), 5*time.Second)
		response, err := fetch(c, ids)
		stop()
		if err != nil {
			return err
		}
		if response == nil || len(response.Snapshots) != len(ids) {
			return errors.New("warmup batch omitted entities")
		}
		for i, snapshot := range response.Snapshots {
			source := d.environment.Sources[d.targets[start+i].source]
			if snapshot == nil || !snapshot.Found || snapshot.EntityId != ids[i] || snapshot.SourceId != source.ID || snapshot.SourceGeneration != source.Generation || snapshot.Version != 1 || snapshot.GetSnapshot().GetEntityType() != source.Adapter {
				return fmt.Errorf("warmup snapshot mismatch %s", ids[i])
			}
			report.WarmupSnapshots++
			if snapshot.ExpiresAt != nil && snapshot.ExpiresAt.AsTime().Before(time.Now()) {
				report.WarmupExpiredSnapshots++
			}
			report.WarmupSnapshotsByType[snapshot.Snapshot.EntityType]++
		}
		if start%10000 == 0 || end == len(d.targets) {
			progress("warmup_snapshots", end, len(d.targets))
		}
	}
	return nil
}

func phaseFailure(p Phase) error {
	if p.Scheduled != p.Requested || p.Accepted != p.Requested || p.Errors > 0 || p.QueueDrops > 0 {
		return fmt.Errorf("incomplete load: requested=%d scheduled=%d accepted=%d errors=%d drops=%d", p.Requested, p.Scheduled, p.Accepted, p.Errors, p.QueueDrops)
	}
	if p.ProjectorLag != 0 || p.HistoryLag != 0 {
		return fmt.Errorf("incomplete consumers: projector=%d history=%d", p.ProjectorLag, p.HistoryLag)
	}
	if p.ObservationMisses > 0 || p.Visible.Samples != p.ObservationCandidates || p.ObservationCandidates == 0 {
		return fmt.Errorf("incomplete observation: candidates=%d samples=%d misses=%d", p.ObservationCandidates, p.Visible.Samples, p.ObservationMisses)
	}
	if p.GenerationElapsed <= 0 || float64(p.Scheduled)/p.GenerationElapsed < .95*float64(p.OfferedRate) {
		return errors.New("under_offered: generator achieved less than 95% of requested rate")
	}
	if p.Elapsed <= 0 || float64(p.Accepted)/p.Elapsed < .95*float64(p.OfferedRate) {
		return errors.New("under_accepted: accepted throughput including drain below 95% of requested rate")
	}
	return nil
}

// 发布结束后同时等待投影与历史消费组。剩余阶段预算优先于 30 秒上限，
// 超时保留最后一次成功观测的积压，不能将探测失败当作零积压。
func drainConsumers(ctx context.Context, budget time.Duration, probe func(context.Context) (int64, int64, error)) (int64, int64, error) {
	ctx, cancel := context.WithTimeout(ctx, min(budget, 30*time.Second))
	defer cancel()
	projector, history := int64(-1), int64(-1)
	for {
		if err := ctx.Err(); err != nil {
			return projector, history, fmt.Errorf("consumer drain: projector=%d history=%d: %w", projector, history, err)
		}
		p, h, err := probe(ctx)
		if err != nil {
			return projector, history, fmt.Errorf("consumer drain probe: %w", err)
		}
		projector, history = p, h
		if projector == 0 && history == 0 {
			return projector, history, nil
		}
		select {
		case <-ctx.Done():
			return projector, history, fmt.Errorf("consumer drain: projector=%d history=%d: %w", projector, history, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}
