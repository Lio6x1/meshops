package taskservicelogic

import (
	"context"

	"github.com/Lio6x1/meshops/app/task/internal/svc"
	taskv1 "github.com/Lio6x1/meshops/gen/task/v1"

	"github.com/zeromicro/go-zero/core/logx"
)

type GetTaskHistoryLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewGetTaskHistoryLogic(ctx context.Context, svcCtx *svc.ServiceContext) *GetTaskHistoryLogic {
	return &GetTaskHistoryLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *GetTaskHistoryLogic) GetTaskHistory(in *taskv1.GetTaskHistoryRequest) (*taskv1.GetTaskHistoryResponse, error) {
	// todo: add your logic here and delete this line

	return &taskv1.GetTaskHistoryResponse{}, nil
}
