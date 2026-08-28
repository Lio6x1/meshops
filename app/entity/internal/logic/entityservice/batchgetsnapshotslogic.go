package entityservicelogic

import (
	"context"

	"github.com/Lio6x1/meshops/app/entity/internal/svc"
	entityv1 "github.com/Lio6x1/meshops/gen/entity/v1"

	"github.com/zeromicro/go-zero/core/logx"
)

type BatchGetSnapshotsLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewBatchGetSnapshotsLogic(ctx context.Context, svcCtx *svc.ServiceContext) *BatchGetSnapshotsLogic {
	return &BatchGetSnapshotsLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

// 批量获取快照（一元）
func (l *BatchGetSnapshotsLogic) BatchGetSnapshots(in *entityv1.BatchGetSnapshotsRequest) (*entityv1.BatchGetSnapshotsResponse, error) {
	// todo: add your logic here and delete this line

	return &entityv1.BatchGetSnapshotsResponse{}, nil
}
