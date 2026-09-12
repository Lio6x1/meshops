package edge

import (
	"context"
	"errors"
	commonv1 "example.com/meshops-course/gen/common/v1"
	ingestv1 "example.com/meshops-course/gen/ingest/v1"
	"fmt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"io"
	"math/rand"
	"sync/atomic"
	"time"
)

type UplinkStats struct {
	Sent       atomic.Int64
	Reconnects atomic.Int64
}

func pause(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
func reconnectDelay(attempt int) time.Duration {
	if attempt > 5 {
		attempt = 5
	}
	base := time.Second * time.Duration(1<<attempt)
	if base > 30*time.Second {
		base = 30 * time.Second
	}
	delay := time.Duration(float64(base) * (0.8 + rand.Float64()*0.4))
	if delay > 30*time.Second {
		delay = 30 * time.Second
	}
	return delay
}

// Upload maintains one in-flight contiguous batch. The ten-second timer cancels
// this stream on a stuck Send/Recv; there is no unary deadline on stream life.
// drain=false 的生命周期由调用者 context 控制：队列暂时为空只等待下一批生成，
// 不代表流已完成。每次重连从本地已确认水位重发，只有 ACK 才能删除持久化前缀。
func Upload(ctx context.Context, q *Queue, client ingestv1.IngestServiceClient, batch, rate int, drain bool, stats *UplinkStats) error {
	if batch < 1 || batch > 100 || rate < 1 {
		return errors.New("invalid batch or recovery rate")
	}
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := uploadStream(ctx, q, client, batch, rate, drain, stats)
		if err == nil {
			return nil
		}
		// 若已经收到明确的鉴权/协议失败，生成器此刻结束并取消 context 也不能
		// 覆盖它；否则 CLI 会把错误当作正常关闭吞掉。可重试的传输失败再服从取消。
		switch status.Code(err) {
		case codes.InvalidArgument, codes.PermissionDenied, codes.Unauthenticated, codes.FailedPrecondition:
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		stats.Reconnects.Add(1)
		if e := pause(ctx, reconnectDelay(attempt)); e != nil {
			return e
		}
	}
}
func uploadStream(ctx context.Context, q *Queue, client ingestv1.IngestServiceClient, batch, rate int, drain bool, stats *UplinkStats) error {
	baseline, err := q.BeginStream()
	if err != nil {
		return err
	}
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stream, err := client.ReportEntityStates(streamCtx)
	if err != nil {
		return err
	}
	for {
		pending, e := q.Pending(batch)
		if e != nil {
			return e
		}
		if len(pending) == 0 {
			if !drain {
				if e = pause(ctx, 25*time.Millisecond); e != nil {
					return e
				}
				continue
			}
			timer := time.AfterFunc(10*time.Second, cancel)
			defer timer.Stop()
			if e = stream.CloseSend(); e != nil {
				return e
			}
			for {
				ack, e := stream.Recv()
				if e == io.EOF {
					return nil
				}
				if e != nil {
					return e
				}
				if e = q.Ack(ack.GatewayEpoch, ack.ConfirmedSequence); e != nil {
					return status.Error(codes.FailedPrecondition, e.Error())
				}
			}
		}
		req := &ingestv1.ReportEntityStatesRequest{GatewayEpoch: baseline.Epoch, ResumeAfterSequence: baseline.ConfirmedSequence, FirstSequence: pending[0].Sequence}
		for _, p := range pending {
			req.Events = append(req.Events, p.Event)
			if proto.Size(req) > 4<<20 {
				req.Events = req.Events[:len(req.Events)-1]
				break
			}
		}
		if len(req.Events) == 0 {
			return status.Error(codes.ResourceExhausted, "single event exceeds 4MiB")
		}
		last := req.FirstSequence + int64(len(req.Events)) - 1
		timer := time.AfterFunc(10*time.Second, cancel)
		if e = stream.Send(req); e != nil {
			timer.Stop()
			return e
		}
		if e = q.MarkSent(req.GatewayEpoch, req.FirstSequence, last); e != nil {
			timer.Stop()
			return e
		}
		stats.Sent.Add(int64(len(req.Events)))
		ack, e := stream.Recv()
		timer.Stop()
		if e != nil {
			return e
		}
		if e = q.Ack(ack.GatewayEpoch, ack.ConfirmedSequence); e != nil {
			return status.Error(codes.FailedPrecondition, e.Error())
		}
		if len(ack.Errors) > 0 || ack.ConfirmedSequence != last {
			return status.Error(codes.Unavailable, fmt.Sprintf("partial batch acknowledged through %d", ack.ConfirmedSequence))
		}
		if e = pause(ctx, time.Second*time.Duration(len(req.Events))/time.Duration(rate)); e != nil {
			return e
		}
	}
}

// CopyEvent is used for intentional duplicates without allocating a new version.
func CopyEvent(e *commonv1.EntityStateEvent) *commonv1.EntityStateEvent {
	return proto.Clone(e).(*commonv1.EntityStateEvent)
}
