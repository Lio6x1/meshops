package main

import (
	"flag"
	"fmt"

	"github.com/Lio6x1/meshops/app/task/internal/config"
	taskserviceServer "github.com/Lio6x1/meshops/app/task/internal/server/taskservice"
	"github.com/Lio6x1/meshops/app/task/internal/svc"
	taskv1 "github.com/Lio6x1/meshops/gen/task/v1"

	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/core/service"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

var configFile = flag.String("f", "etc/meshops.task.v1.yaml", "the config file")

func main() {
	flag.Parse()

	var c config.Config
	conf.MustLoad(*configFile, &c)
	ctx := svc.NewServiceContext(c)

	s := zrpc.MustNewServer(c.RpcServerConf, func(grpcServer *grpc.Server) {
		taskv1.RegisterTaskServiceServer(grpcServer, taskserviceServer.NewTaskServiceServer(ctx))

		if c.Mode == service.DevMode || c.Mode == service.TestMode {
			reflection.Register(grpcServer)
		}
	})
	defer s.Stop()

	fmt.Printf("Starting rpc server at %s...\n", c.ListenOn)
	s.Start()
}
