package edge

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	ingestv1 "example.com/meshops-course/gen/ingest/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type gatedUploadFailure struct {
	ingestv1.IngestServiceClient
	entered chan struct{}
	release chan struct{}
	err     error
}

func (c *gatedUploadFailure) ReportEntityStates(context.Context, ...grpc.CallOption) (ingestv1.IngestService_ReportEntityStatesClient, error) {
	close(c.entered)
	<-c.release
	return nil, c.err
}

func TestUploadPreservesFatalFailureDuringShutdown(t *testing.T) {
	for _, code := range []codes.Code{codes.InvalidArgument, codes.PermissionDenied, codes.Unauthenticated, codes.FailedPrecondition, codes.Unavailable, codes.Canceled} {
		t.Run(code.String(), func(t *testing.T) {
			q, err := OpenQueue(filepath.Join(t.TempDir(), "queue.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer q.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client := &gatedUploadFailure{entered: make(chan struct{}), release: make(chan struct{}), err: status.Error(code, "returned upload failure")}
			done := make(chan error, 1)
			go func() { done <- Upload(ctx, q, client, 1, 1, false, new(UplinkStats)) }()
			select {
			case <-client.entered:
			case <-time.After(time.Second):
				close(client.release)
				t.Fatal("Upload did not enter stream setup")
			}
			// 生成器结束时，流建立过程已发生明确错误。
			// 验证 CLI 等待上传结果前，Upload 的错误选择逻辑。
			cancel()
			close(client.release)
			select {
			case err = <-done:
			case <-time.After(time.Second):
				t.Fatal("Upload failed to join after shutdown")
			}
			if code == codes.Unavailable || code == codes.Canceled {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("retryable transport error did not respect shutdown: %v", err)
				}
			} else if status.Code(err) != code {
				t.Fatalf("fatal failure replaced by shutdown: got %v want %v", err, code)
			}
		})
	}
}
