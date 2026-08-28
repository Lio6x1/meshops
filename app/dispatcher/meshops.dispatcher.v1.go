package main

import (
	"flag"
	"fmt"

	"github.com/Lio6x1/meshops/app/dispatcher/internal/config"
	dispatcherserviceServer "github.com/Lio6x1/meshops/app/dispatcher/internal/server/dispatcherservice"
	"github.com/Lio6x1/meshops/app/dispatcher/internal/svc"
	dispatcherv1 "github.com/Lio6x1/meshops/gen/dispatcher/v1"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/service"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

var configFile = flag.String("f", "etc/meshops.dispatcher.v1.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c)
	ctx := svc.NewServiceContext(c)

	s := zrpc.MustNewServer(c.RpcServerConf, func(grpcServer *grpc.Server) {
		dispatcherv1.RegisterDispatcherServiceServer(grpcServer, dispatcherserviceServer.NewDispatcherServiceServer(ctx))

		if c.Mode == service.DevMode || c.Mode == service.TestMode {
			reflection.Register(grpcServer)
		}
	})
	defer s.Stop()

	fmt.Printf("Starting rpc server at %s...\n", c.ListenOn)
	s.Start()
}
