package main

import (
	"flag"
	"fmt"

	"github.com/Lio6x1/meshops/app/ingest/internal/config"
	ingestserviceServer "github.com/Lio6x1/meshops/app/ingest/internal/server/ingestservice"
	"github.com/Lio6x1/meshops/app/ingest/internal/svc"
	ingestv1 "github.com/Lio6x1/meshops/gen/ingest/v1"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/service"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

var configFile = flag.String("f", "etc/meshops.ingest.v1.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c)
	ctx := svc.NewServiceContext(c)

	s := zrpc.MustNewServer(c.RpcServerConf, func(grpcServer *grpc.Server) {
		ingestv1.RegisterIngestServiceServer(grpcServer, ingestserviceServer.NewIngestServiceServer(ctx))

		if c.Mode == service.DevMode || c.Mode == service.TestMode {
			reflection.Register(grpcServer)
		}
	})
	defer s.Stop()

	fmt.Printf("Starting rpc server at %s...\n", c.ListenOn)
	s.Start()
}
