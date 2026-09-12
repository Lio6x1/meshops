package config

import "github.com/zeromicro/go-zero/zrpc"

// Embedding exposes Name, ListenOn and the framework configuration fields.
type Config struct {
 zrpc.RpcServerConf
}
