package cli

import (
	"bytes"
	"context"
	"net"
	"strings"
	"testing"

	searchv1 "example.com/meshops-course/gen/search/v1"
	"google.golang.org/grpc"
)

type searchServer struct {
	searchv1.UnimplementedSearchServiceServer
	requests chan *searchv1.SearchTasksRequest
}

func (s *searchServer) SearchTasks(_ context.Context, r *searchv1.SearchTasksRequest) (*searchv1.SearchTasksResponse, error) {
	s.requests <- r
	return &searchv1.SearchTasksResponse{Tasks: []*searchv1.TaskSearchHit{{TaskId: "real-search-response"}}}, nil
}

func TestOpctlSearchCallsSearchRPC(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := grpc.NewServer()
	handler := &searchServer{requests: make(chan *searchv1.SearchTasksRequest, 1)}
	searchv1.RegisterSearchServiceServer(s, handler)
	go s.Serve(l)
	defer s.Stop()
	t.Setenv("MESHOPS_OPERATOR_TOKEN", strings.Repeat("t", 32))
	var out, diagnostics bytes.Buffer
	code := Opctl(context.Background(), []string{"task", "search", "--endpoint", l.Addr().String(), "--keyword", "屋顶", "--entity", "drone-001", "--page-size", "5"}, &out, &diagnostics)
	if code != 0 || !strings.Contains(out.String(), "real-search-response") {
		t.Fatalf("code %d out %s err %s", code, out.String(), diagnostics.String())
	}
	r := <-handler.requests
	if r.Keyword != "屋顶" || r.TargetEntityId != "drone-001" || r.PageSize != 5 {
		t.Fatal(r)
	}
}
