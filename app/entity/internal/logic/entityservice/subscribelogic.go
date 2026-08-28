package entityservicelogic

import (
	"context"

	"github.com/Lio6x1/meshops/app/entity/internal/svc"
	entityv1 "github.com/Lio6x1/meshops/gen/entity/v1"

	"github.com/zeromicro/go-zero/core/logx"
)

type SubscribeLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewSubscribeLogic(ctx context.Context, svcCtx *svc.ServiceContext) *SubscribeLogic {
	return &SubscribeLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

// 订阅实体更新（服务端流）
func (l *SubscribeLogic) Subscribe(in *entityv1.SubscribeRequest, stream entityv1.EntityService_SubscribeServer) error {
	// todo: add your logic here and delete this line

	return nil
}
