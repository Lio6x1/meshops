package main

import (
	"flag"
	"fmt"

	"github.com/Lio6x1/meshops/app/entity/internal/config"
	entityserviceServer "github.com/Lio6x1/meshops/app/entity/internal/server/entityservice"
	"github.com/Lio6x1/meshops/app/entity/internal/svc"
	entityv1 "github.com/Lio6x1/meshops/gen/entity/v1"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/service"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

var configFile = flag.String("f", "etc/meshops.entity.v1.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c)
	ctx := svc.NewServiceContext(c)

	s := zrpc.MustNewServer(c.RpcServerConf, func(grpcServer *grpc.Server) {
		entityv1.RegisterEntityServiceServer(grpcServer, entityserviceServer.NewEntityServiceServer(ctx))

		if c.Mode == service.DevMode || c.Mode == service.TestMode {
			reflection.Register(grpcServer)
		}
	})
	defer s.Stop()

	fmt.Printf("Starting rpc server at %s...\n", c.ListenOn)
	s.Start()
}
