package edge

import (
	"context"
	"errors"
	commonv1 "example.com/meshops-course/gen/common/v1"
	"example.com/meshops-course/internal/simulation"
	"fmt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"hash/fnv"
	"io"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"time"
)

// simulateMotion 为归一化测试数据加入有界、可复现的运动与电量变化，
// 不会凭空补出缺失的组件或能力。
// 观测时间仍使用真实时钟，事件标识仍按每次观测新生成。
func simulateMotion(event *commonv1.EntityStateEvent, rng *rand.Rand) {
	if event == nil || event.Snapshot == nil {
		return
	}
	s := event.Snapshot
	if s.Location != nil {
		// 在不改变来源坐标系的前提下，分开各组测试数据的位置。
		// 静态设备保持固定锚点；移动类型使用事件时间。
		h := fnv.New32a()
		_, _ = h.Write([]byte(event.SourceId + ":" + event.EntityId))
		bearing := float64(h.Sum32()%360) * math.Pi / 180
		lat, lon := .0015*math.Sin(bearing), .0015*math.Cos(bearing)
		// 六个演示站点排成两行，为地图标记与标签
		// 留出足够空间。即使只有这六个 ID，纯哈希锚点也可能碰撞。
		// 未知类型在自身测试数据附近保留确定性的兜底位置。
		switch s.EntityType {
		case "person":
			lat, lon = .0015, -.003
		case "drone":
			lat, lon = .0015, 0
		case "vehicle":
			lat, lon = .0015, .003
		case "robot":
			lat, lon = -.0015, -.003
		case "sensor":
			lat, lon = -.0015, 0
		case "facility":
			lat, lon = -.0015, .003
		}
		// 每种类型有五个独立注册的测试实体。将 -001 保留在
		// 原站点，并按真实坐标将 -002..005 放置在周围。
		// 此偏移与版本无关，因此固定设施不会移动。
		if suffix := strings.LastIndexByte(event.EntityId, '-'); suffix >= 0 {
			if n, err := strconv.Atoi(event.EntityId[suffix+1:]); err == nil && n >= 2 && n <= simulation.MaxEntitiesPerSource {
				angle := float64(n-2)*math.Pi/2 + math.Pi/4
				lat += .0008 * math.Sin(angle)
				lon += .0008 * math.Cos(angle)
			}
		}
		if s.EntityType != "sensor" && s.EntityType != "facility" {
			seconds := float64(event.EntityVersion%120000) / 2 // 无时间戳单元测试数据的确定性兜底值
			if event.OccurredAt != nil && event.OccurredAt.CheckValid() == nil {
				seconds = float64(event.OccurredAt.AsTime().UnixMilli()) / 1000
			}
			offset := float64(h.Sum32()%1000) / 1000
			if suffix := strings.LastIndexByte(event.EntityId, '-'); suffix >= 0 {
				if n, err := strconv.Atoi(event.EntityId[suffix+1:]); err == nil && n >= 1 && n <= simulation.MaxEntitiesPerSource {
					offset = float64(n-1) / simulation.MaxEntitiesPerSource
				}
			}
			x, y := demoRoute(s.EntityType, seconds, offset)
			lat, lon = (320-y)/111320, (x-500)/(111320*math.Cos(s.Location.Latitude*math.Pi/180))
			if s.Velocity != nil {
				nx, ny := demoRoute(s.EntityType, seconds+.01, offset)
				s.Velocity.Speed = math.Hypot(nx-x, ny-y) / .01
				heading := math.Mod(math.Atan2(nx-x, y-ny)*180/math.Pi+360, 360)
				s.Velocity.Heading = &heading
			}
		}
		s.Location.Latitude = math.Max(-90, math.Min(90, s.Location.Latitude+lat))
		s.Location.Longitude = math.Max(-180, math.Min(180, s.Location.Longitude+lon))
	}
	if s.EntityType == "sensor" || s.EntityType == "facility" {
		return
	}
	if s.Power != nil && s.Power.BatteryPercent != nil {
		battery := math.Max(0, math.Min(100, *s.Power.BatteryPercent+(rng.Float64()-.5)*2))
		s.Power.BatteryPercent = &battery
	}
}

type simulationControlStore interface {
	Desired(context.Context, string, string) (simulation.Mode, error)
	DesiredCount(context.Context, string, string) (int, error)
	Observe(context.Context, string, string, simulation.Observation) error
}

// runControlledSimulation 管理唯一的上传工作协程。状态切换确认
// 只在旧流取消且退出后发布。暂停状态停止
// 生成与传输；离线状态则让持久化队列继续增长。
func runControlledSimulation(ctx context.Context, store simulationControlStore, tenant, source string, capacity int, pollInterval, generateInterval time.Duration, generate func(int) error, upload func(context.Context) error, observation func() simulation.Observation, diagnostic io.Writer) (runErr error) {
	mode := simulation.Paused
	activeCount := 0
	var stopUpload context.CancelFunc
	var uploadDone chan error
	shutdownResult := func(err error) error {
		if ctx.Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || status.Code(err) == codes.Canceled || status.Code(err) == codes.DeadlineExceeded) {
			return nil
		}
		return err
	}
	stop := func() error {
		if stopUpload == nil {
			return nil
		}
		stopUpload()
		err := joinUpload(uploadDone)
		stopUpload = nil
		uploadDone = nil
		return shutdownResult(err)
	}
	defer func() {
		if err := stop(); runErr == nil {
			runErr = err
		}
	}()
	refresh := func() error {
		controlCtx, cancel := context.WithTimeout(ctx, time.Second)
		desired, err := store.Desired(controlCtx, tenant, source)
		count := 0
		if err == nil {
			count, err = store.DesiredCount(controlCtx, tenant, source)
		}
		cancel()
		if err != nil {
			fmt.Fprintln(diagnostic, "simulation control unavailable; pausing source:", err)
			desired = simulation.Paused
			count = 0
		}
		if !desired.Valid() {
			return fmt.Errorf("invalid desired simulation mode %q", desired)
		}
		if count < 0 || count > simulation.MaxEntitiesPerSource {
			return fmt.Errorf("invalid desired simulation count %d", count)
		}
		// 显式启用的自定义清单可能不足五个 ID。只确认
		// 实际选中的子集，不能假装缺失的 ID 存在。
		activeCount = min(count, capacity)
		if desired != mode {
			if err := stop(); err != nil {
				return err
			}
			mode = desired
			if mode == simulation.Running {
				var uploadCtx context.Context
				uploadCtx, stopUpload = context.WithCancel(ctx)
				uploadDone = make(chan error, 1)
				go func(done chan<- error) { done <- upload(uploadCtx) }(uploadDone)
			}
		}
		o := observation()
		o.Applied = mode
		o.ActiveCount = activeCount
		controlCtx, cancel = context.WithTimeout(ctx, time.Second)
		err = store.Observe(controlCtx, tenant, source, o)
		cancel()
		if err != nil {
			fmt.Fprintln(diagnostic, "simulation heartbeat unavailable; pausing source:", err)
			if stopErr := stop(); stopErr != nil {
				return stopErr
			}
			mode = simulation.Paused
		}
		return nil
	}
	if err := refresh(); err != nil {
		return err
	}
	controlTick := time.NewTicker(pollInterval)
	defer controlTick.Stop()
	generateTick := time.NewTicker(generateInterval)
	defer generateTick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-uploadDone:
			stopUpload()
			stopUpload = nil
			uploadDone = nil
			// 取消与工作协程结果可能同时就绪。select 的
			// 选择顺序不能把正常的 count/duration 停止变成失败。
			return shutdownResult(err)
		case <-controlTick.C:
			if err := refresh(); err != nil {
				return err
			}
		case <-generateTick.C:
			if mode != simulation.Paused && activeCount > 0 {
				if err := generate(activeCount); err != nil {
					return err
				}
			}
		}
	}
}
