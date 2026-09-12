package edge

import (
	"context"
	ingestv1 "example.com/meshops-course/gen/ingest/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
	"io"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type uploadServer struct {
	ingestv1.UnimplementedIngestServiceServer
	mu         sync.Mutex
	requests   []*ingestv1.ReportEntityStatesRequest
	lostFirst  bool
	wrongEpoch bool
	partial    bool
	closed     bool
	hold       time.Duration
}

func (s *uploadServer) ReportEntityStates(stream ingestv1.IngestService_ReportEntityStatesServer) error {
	var ack *ingestv1.ReportEntityStatesResponse
	for {
		r, e := stream.Recv()
		if e == io.EOF {
			s.mu.Lock()
			s.closed = true
			s.mu.Unlock()
			if ack != nil {
				return stream.Send(ack)
			}
			return nil
		}
		if e != nil {
			return e
		}
		s.mu.Lock()
		s.requests = append(s.requests, proto.Clone(r).(*ingestv1.ReportEntityStatesRequest))
		n := len(s.requests)
		s.mu.Unlock()
		if s.hold > 0 {
			select {
			case <-time.After(s.hold):
			case <-stream.Context().Done():
				return stream.Context().Err()
			}
		}
		if s.lostFirst && n == 1 {
			return status.Error(codes.Unavailable, "simulated response loss")
		}
		ack = &ingestv1.ReportEntityStatesResponse{GatewayEpoch: r.GatewayEpoch, ConfirmedSequence: r.FirstSequence + int64(len(r.Events)) - 1}
		if s.wrongEpoch {
			ack.GatewayEpoch = "wrong"
		}
		if s.partial && n == 1 {
			ack.ConfirmedSequence = r.FirstSequence
			ack.Errors = []*ingestv1.EventError{{Sequence: r.FirstSequence + 1}}
			if e = stream.Send(ack); e != nil {
				return e
			}
			return status.Error(codes.Unavailable, "partial failure")
		}
		if e = stream.Send(ack); e != nil {
			return e
		}
	}
}
func uploadClient(t *testing.T, s *uploadServer) ingestv1.IngestServiceClient {
	t.Helper()
	listener := bufconn.Listen(4 << 20)
	server := grpc.NewServer()
	ingestv1.RegisterIngestServiceServer(server, s)
	go server.Serve(listener)
	conn, e := grpc.NewClient("passthrough:///memory", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { conn.Close(); server.Stop(); listener.Close() })
	return ingestv1.NewIngestServiceClient(conn)
}
func TestUploadACKLossAndCloseSendFinalACK(t *testing.T) {
	s := &uploadServer{lostFirst: true}
	client := uploadClient(t, s)
	q, e := OpenQueue(filepath.Join(t.TempDir(), "queue.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	generate(t, q)
	generate(t, q)
	stats := new(UplinkStats)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if e = Upload(ctx, q, client, 100, 500, true, stats); e != nil {
		t.Fatal(e)
	}
	snapshot, _ := q.Stats()
	if snapshot.Pending != 0 || snapshot.ConfirmedSequence != 2 || stats.Sent.Load() != 4 {
		t.Fatal(snapshot, stats.Sent.Load())
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed || len(s.requests) != 2 || !proto.Equal(s.requests[0], s.requests[1]) {
		t.Fatal("replay identity or CloseSend failed")
	}
}
func TestUploadPartialACKReplaysOnlySuffix(t *testing.T) {
	s := &uploadServer{partial: true}
	client := uploadClient(t, s)
	q, e := OpenQueue(filepath.Join(t.TempDir(), "queue.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	for n := 0; n < 3; n++ {
		generate(t, q)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if e = Upload(ctx, q, client, 100, 500, true, new(UplinkStats)); e != nil {
		t.Fatal(e)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.requests) != 2 || s.requests[1].FirstSequence != 2 || s.requests[1].ResumeAfterSequence != 1 || len(s.requests[1].Events) != 2 {
		t.Fatal(s.requests)
	}
}
func TestUploadRejectsInvalidACKWithoutDeleting(t *testing.T) {
	s := &uploadServer{wrongEpoch: true}
	client := uploadClient(t, s)
	q, e := OpenQueue(filepath.Join(t.TempDir(), "queue.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	generate(t, q)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if e = Upload(ctx, q, client, 100, 500, true, new(UplinkStats)); status.Code(e) != codes.FailedPrecondition {
		t.Fatal(e)
	}
	snapshot, _ := q.Stats()
	if snapshot.Pending != 1 || snapshot.ConfirmedSequence != 0 {
		t.Fatal(snapshot)
	}
}
func TestUploadStreamIsNotGivenUnaryFiveSecondDeadline(t *testing.T) {
	s := &uploadServer{hold: 5200 * time.Millisecond}
	client := uploadClient(t, s)
	q, e := OpenQueue(filepath.Join(t.TempDir(), "queue.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	generate(t, q)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if e = Upload(ctx, q, client, 100, 500, true, new(UplinkStats)); e != nil {
		t.Fatal(e)
	}
}
func TestUploadCancellationUnblocksReceive(t *testing.T) {
	s := &uploadServer{hold: time.Minute}
	client := uploadClient(t, s)
	q, e := OpenQueue(filepath.Join(t.TempDir(), "queue.db"))
	if e != nil {
		t.Fatal(e)
	}
	defer q.Close()
	generate(t, q)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	if e = Upload(ctx, q, client, 100, 500, true, new(UplinkStats)); e == nil {
		t.Fatal("expected cancellation")
	}
	if time.Since(started) > time.Second {
		t.Fatal("stream did not cancel promptly")
	}
	snapshot, _ := q.Stats()
	if snapshot.Pending != 1 {
		t.Fatal(snapshot)
	}
}
func TestBackoffIsBounded(t *testing.T) {
	for n := 0; n < 100; n++ {
		d := reconnectDelay(n)
		if d < 800*time.Millisecond || d > 30*time.Second {
			t.Fatal(d)
		}
	}
}
