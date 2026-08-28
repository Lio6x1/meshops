package dispatcherservicelogic

import (
	"context"

	"github.com/Lio6x1/meshops/app/dispatcher/internal/svc"
	dispatcherv1 "github.com/Lio6x1/meshops/gen/dispatcher/v1"

	"github.com/zeromicro/go-zero/core/logx"
)

type RetryDLQLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewRetryDLQLogic(ctx context.Context, svcCtx *svc.ServiceContext) *RetryDLQLogic {
	return &RetryDLQLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

// 手动重试死信任务（一元）
func (l *RetryDLQLogic) RetryDLQ(in *dispatcherv1.RetryDLQRequest) (*dispatcherv1.RetryDLQResponse, error) {
	// todo: add your logic here and delete this line

	return &dispatcherv1.RetryDLQResponse{}, nil
}
