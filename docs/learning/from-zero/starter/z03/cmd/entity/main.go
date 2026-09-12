package main

import (
	"flag"
	"fmt"
	"log"

	entityv1 "example.com/meshops-course/gen/entity/v1"
	"example.com/meshops-course/internal/config"
	"example.com/meshops-course/internal/service"
	"github.com/zeromicro/go-zero/core/conf"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"
)

func main() {
	file := flag.String("f", "configs/entity.yaml", "path to server YAML")
	flag.Parse()
	var c config.Config
	if err := conf.Load(*file, &c); err != nil {
		log.Fatal(err)
	}
	server, err := zrpc.NewServer(c.RpcServerConf, func(g *grpc.Server) {
		entityv1.RegisterEntityServiceServer(g, service.New())
	})
	if err != nil {
		log.Fatal(err)
	}
	defer server.Stop()
	fmt.Printf("Entity listening on %s\n", c.ListenOn)
	server.Start()
}
