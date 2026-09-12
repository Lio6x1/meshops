package search

import (
	"context"
	"errors"
	"strings"
	"time"

	commonv1 "example.com/meshops-course/gen/common/v1"
	searchv1 "example.com/meshops-course/gen/search/v1"
	"example.com/meshops-course/internal/platform"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type QueryIndex interface {
	Search(context.Context, string, Filter, string, []byte) (Page, error)
}

type Service struct {
	searchv1.UnimplementedSearchServiceServer
	Index     QueryIndex
	CursorKey []byte
	Ready     func() bool
}

func (s *Service) SearchTasks(ctx context.Context, request *searchv1.SearchTasksRequest) (*searchv1.SearchTasksResponse, error) {
	started := time.Now()
	defer func() { queryDuration.Observe(time.Since(started).Seconds()) }()
	p := platform.Identity(ctx)
	if p.TenantID == "" || (p.Role != "operator" && p.Role != "admin") {
		return nil, status.Error(codes.PermissionDenied, "operator identity required")
	}
	if request == nil {
		return nil, status.Error(codes.InvalidArgument, "request required")
	}
	if s.Index == nil || s.Ready == nil || !s.Ready() {
		return nil, status.Error(codes.Unavailable, "search projection not ready")
	}
	f := Filter{Keyword: request.Keyword, TargetEntityID: request.TargetEntityId, PageSize: int(request.PageSize)}
	if request.Status != commonv1.TaskStatus_TASK_STATUS_UNSPECIFIED {
		f.Status = strings.TrimPrefix(request.Status.String(), "TASK_STATUS_")
	}
	for _, v := range []struct {
		src *timestamppb.Timestamp
		dst *string
	}{{request.CreatedFrom, &f.CreatedFrom}, {request.CreatedBefore, &f.CreatedBefore}} {
		if v.src != nil {
			if v.src.CheckValid() != nil {
				return nil, status.Error(codes.InvalidArgument, "invalid creation timestamp")
			}
			*v.dst = v.src.AsTime().UTC().Format(time.RFC3339Nano)
		}
	}
	if _, _, err := normalizeFilter(f); err != nil {
		return nil, status.Error(codes.InvalidArgument, ErrInvalidSearch.Error())
	}
	page, err := s.Index.Search(ctx, p.TenantID, f, request.PageToken, s.CursorKey)
	if err != nil {
		if ctx.Err() != nil {
			return nil, status.FromContextError(ctx.Err()).Err()
		}
		if errors.Is(err, ErrInvalidSearch) {
			return nil, status.Error(codes.InvalidArgument, ErrInvalidSearch.Error())
		}
		if errors.Is(err, ErrSearchExpired) {
			return nil, status.Error(codes.FailedPrecondition, ErrSearchExpired.Error())
		}
		return nil, status.Error(codes.Unavailable, "search dependency unavailable")
	}
	response := &searchv1.SearchTasksResponse{NextPageToken: page.NextCursor}
	for _, d := range page.Tasks {
		created, e1 := time.Parse(time.RFC3339Nano, d.CreatedAt)
		updated, e2 := time.Parse(time.RFC3339Nano, d.UpdatedAt)
		value, ok := commonv1.TaskStatus_value["TASK_STATUS_"+d.Status]
		if d.TenantID != p.TenantID || e1 != nil || e2 != nil || !ok || value == 0 || d.StatusVersion < 0 || d.StatusVersion > 2147483647 {
			return nil, status.Error(codes.Unavailable, "invalid search projection")
		}
		response.Tasks = append(response.Tasks, &searchv1.TaskSearchHit{TaskId: d.TaskID, TargetEntityId: d.TargetEntityID, TaskType: d.TaskType, Status: commonv1.TaskStatus(value), StatusVersion: int32(d.StatusVersion), Note: d.Note, CancelledReason: d.CancelledReason, FailureReason: d.FailureReason, CreatedAt: timestamppb.New(created), UpdatedAt: timestamppb.New(updated)})
	}
	return response, nil
}
