package taskservicelogic

import (
	"context"

	"github.com/Lio6x1/meshops/app/task/internal/svc"
	taskv1 "github.com/Lio6x1/meshops/gen/task/v1"

	"github.com/zeromicro/go-zero/core/logx"
)

type GetTaskLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewGetTaskLogic(ctx context.Context, svcCtx *svc.ServiceContext) *GetTaskLogic {
	return &GetTaskLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *GetTaskLogic) GetTask(in *taskv1.GetTaskRequest) (*taskv1.GetTaskResponse, error) {
	// todo: add your logic here and delete this line

	return &taskv1.GetTaskResponse{}, nil
}
