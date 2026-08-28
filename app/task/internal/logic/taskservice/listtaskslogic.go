package taskservicelogic

import (
	"context"

	"github.com/Lio6x1/meshops/app/task/internal/svc"
	taskv1 "github.com/Lio6x1/meshops/gen/task/v1"

	"github.com/zeromicro/go-zero/core/logx"
)

type ListTasksLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewListTasksLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ListTasksLogic {
	return &ListTasksLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *ListTasksLogic) ListTasks(in *taskv1.ListTasksRequest) (*taskv1.ListTasksResponse, error) {
	// todo: add your logic here and delete this line

	return &taskv1.ListTasksResponse{}, nil
}
