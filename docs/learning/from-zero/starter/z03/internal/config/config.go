package config

import "github.com/zeromicro/go-zero/zrpc"

// 嵌入配置类型，使 Name、ListenOn 及框架配置字段可直接使用。
type Config struct {
	zrpc.RpcServerConf
}
