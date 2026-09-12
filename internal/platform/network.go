package platform

import (
	"errors"
	"net"
	"os"
	"strconv"
)

// DemoNetwork 仅在显式启用时允许访问 Compose 演示的私有网络。
// 它不是生产环境的传输安全开关：RPC 和依赖服务端口
// 仍不得公开。配置值拼写未知时直接拒绝，避免悄悄放宽
// 默认仅限本机的教学配置。
func DemoNetwork() (bool, error) {
	switch os.Getenv("MESHOPS_NETWORK_MODE") {
	case "", "loopback":
		return false, nil
	case "compose-demo":
		return true, nil
	default:
		return false, errors.New("unknown MESHOPS_NETWORK_MODE")
	}
}

// localRPCAddress 在演示模式下额外允许一组固定服务地址。
// 校验原始服务地址，不接受调用方自定义的 gRPC 解析器 URI。
func localRPCAddress(address string, listener bool) error {
	demo, err := DemoNetwork()
	if err != nil {
		return err
	}
	host, port, err := net.SplitHostPort(address)
	n, portErr := strconv.Atoi(port)
	if err != nil || portErr != nil || n < 0 || n > 65535 || (!listener && n == 0) {
		return errors.New("RPC address requires a valid host and port")
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	if demo {
		if listener && host == "0.0.0.0" && n >= 50051 && n <= 50055 {
			return nil
		}
		if !listener {
			switch address {
			case "ingest:50051", "entity:50052", "task:50053", "dispatcher:50054", "search:50055":
				return nil
			}
		}
	}
	return errors.New("plaintext RPC requires loopback or an explicitly allowed Compose demo service")
}

// AllowedESAuthority 保留 Search 客户端的 HTTP 来源检查，
// 同时复用 RPC 的显式网络策略；仍然禁止任意 DNS 名称。
func AllowedESAuthority(host string, port int) bool {
	demo, err := DemoNetwork()
	if err != nil || port < 1 || port > 65535 {
		return false
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return true
	}
	return demo && host == "elasticsearch" && port == 9200
}
