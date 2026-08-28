package ingestservicelogic

import (
	"context"

	"github.com/Lio6x1/meshops/app/ingest/internal/svc"
	ingestv1 "github.com/Lio6x1/meshops/gen/ingest/v1"

	"github.com/zeromicro/go-zero/core/logx"
)

type ReportEntityStatesLogic struct {
	ctx    context.Context
	svcCtx *svc.ServiceContext
	logx.Logger
}

func NewReportEntityStatesLogic(ctx context.Context, svcCtx *svc.ServiceContext) *ReportEntityStatesLogic {
	return &ReportEntityStatesLogic{
		ctx:    ctx,
		svcCtx: svcCtx,
		Logger: logx.WithContext(ctx),
	}
}

func (l *ReportEntityStatesLogic) ReportEntityStates(stream ingestv1.IngestService_ReportEntityStatesServer) error {
	// todo: add your logic here and delete this line

	return nil
}
