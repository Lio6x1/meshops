package platform

import (
	"context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"strings"
)

func Outgoing(ctx context.Context, token string) context.Context {
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	md.Set("authorization", "Bearer "+token)
	return metadata.NewOutgoingContext(ctx, md)
}
func Dial(endpoint string) (*grpc.ClientConn, error) {
	if err := localRPCAddress(endpoint, false); err != nil {
		return nil, err
	}
	return grpc.NewClient(endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(4<<20), grpc.MaxCallSendMsgSize(4<<20)))
}
func permitted(role, method string) bool {
	entity := strings.HasPrefix(method, "/meshops.entity.v1.EntityService/")
	task := strings.HasPrefix(method, "/meshops.task.v1.TaskService/")
	dispatch := strings.HasPrefix(method, "/meshops.dispatcher.v1.DispatcherService/")
	search := method == "/meshops.search.v1.SearchService/SearchTasks"
	suffix := method[strings.LastIndex(method, "/")+1:]
	switch role {
	case "source":
		return method == "/meshops.ingest.v1.IngestService/ReportEntityStates"
	case "operator":
		return search || entity || (task && suffix != "ReportTaskStatus") || (dispatch && suffix == "GetDispatch")
	case "admin":
		return search || entity || (task && suffix != "ReportTaskStatus") || dispatch
	case "executor":
		return method == "/meshops.executor.v1.ExecutorService/ListenTasks" || (task && (suffix == "GetTask" || suffix == "ReportTaskStatus"))
	case "task_service":
		return (entity && (suffix == "GetSnapshot" || suffix == "BatchGetSnapshots")) || (dispatch && suffix == "GetDispatch")
	case "dispatcher_service":
		return task && (suffix == "GetTask" || suffix == "ReportTaskStatus")
	}
	return false
}
func (r *Registry) authorize(ctx context.Context, method string) (context.Context, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	values := md.Get("authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
		return nil, status.Error(codes.Unauthenticated, "credential required")
	}
	p, err := r.Authenticate(strings.TrimPrefix(values[0], "Bearer "))
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid credential")
	}
	if !permitted(p.Role, method) {
		return nil, status.Error(codes.PermissionDenied, "role cannot call method")
	}
	return WithPrincipal(ctx, p), nil
}
func (r *Registry) Unary() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		ctx, err := r.authorize(ctx, info.FullMethod)
		if err != nil {
			return nil, err
		}
		return next(ctx, req)
	}
}

type authenticatedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *authenticatedStream) Context() context.Context { return s.ctx }
func (r *Registry) Stream() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, next grpc.StreamHandler) error {
		ctx, err := r.authorize(ss.Context(), info.FullMethod)
		if err != nil {
			return err
		}
		return next(srv, &authenticatedStream{ss, ctx})
	}
}
