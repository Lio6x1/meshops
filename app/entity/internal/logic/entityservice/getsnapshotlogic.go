package entityservicelogic

import (
	"context"

	"github.com/Lio6x1/meshops/app/entity/internal/svc"
	entityv1 "github.com/Lio6x1/meshops/gen/entity/v1"

	"github.com/zeromicro/go-zero/core/logx"
)

type GetSnapshotLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewGetSnapshotLogic(ctx context.Context, svcCtx *svc.ServiceContext) *GetSnapshotLogic {
	return &GetSnapshotLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

// 获取单个快照（一元）
func (l *GetSnapshotLogic) GetSnapshot(in *entityv1.GetSnapshotRequest) (*entityv1.GetSnapshotResponse, error) {
	// todo: add your logic here and delete this line

	return &entityv1.GetSnapshotResponse{}, nil
}
