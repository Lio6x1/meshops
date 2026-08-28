package taskservicelogic

import (
	"context"

	"github.com/Lio6x1/meshops/app/task/internal/svc"
	taskv1 "github.com/Lio6x1/meshops/gen/task/v1"

	"github.com/zeromicro/go-zero/core/logx"
)

type CancelTaskLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewCancelTaskLogic(ctx context.Context, svcCtx *svc.ServiceContext) *CancelTaskLogic {
	return &CancelTaskLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *CancelTaskLogic) CancelTask(in *taskv1.CancelTaskRequest) (*taskv1.CancelTaskResponse, error) {
	// todo: add your logic here and delete this line

	return &taskv1.CancelTaskResponse{}, nil
}
