package dispatcherservicelogic

import (
	"context"

	"github.com/Lio6x1/meshops/app/dispatcher/internal/svc"
	dispatcherv1 "github.com/Lio6x1/meshops/gen/dispatcher/v1"

	"github.com/zeromicro/go-zero/core/logx"
)

type GetDispatchLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewGetDispatchLogic(ctx context.Context, svcCtx *svc.ServiceContext) *GetDispatchLogic {
	return &GetDispatchLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

// 查询下发记录（一元）
func (l *GetDispatchLogic) GetDispatch(in *dispatcherv1.GetDispatchRequest) (*dispatcherv1.GetDispatchResponse, error) {
	// todo: add your logic here and delete this line

	return &dispatcherv1.GetDispatchResponse{}, nil
}
