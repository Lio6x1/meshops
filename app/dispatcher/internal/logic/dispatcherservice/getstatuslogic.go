package dispatcherservicelogic

import (
	"context"

	"github.com/Lio6x1/meshops/app/dispatcher/internal/svc"
	dispatcherv1 "github.com/Lio6x1/meshops/gen/dispatcher/v1"

	"github.com/zeromicro/go-zero/core/logx"
)

type GetStatusLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewGetStatusLogic(ctx context.Context, svcCtx *svc.ServiceContext) *GetStatusLogic {
	return &GetStatusLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

// 查询分发器运行状态（一元，供监控/运维）
func (l *GetStatusLogic) GetStatus(in *dispatcherv1.GetStatusRequest) (*dispatcherv1.GetStatusResponse, error) {
	// todo: add your logic here and delete this line

	return &dispatcherv1.GetStatusResponse{}, nil
}
