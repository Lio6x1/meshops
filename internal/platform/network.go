package platform

import (
	"errors"
	"net"
	"os"
	"strconv"
)

// DemoNetwork is an explicit opt-in for the private Compose demo network.
// It is not a production transport security switch: RPC and dependency ports
// must remain unpublished. Unknown spellings fail closed instead of weakening
// the host-local teaching configuration silently.
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

// localRPCAddress also admits a fixed set of service authorities in demo mode.
// Validate the original authority, never a caller-supplied gRPC resolver URI.
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

// AllowedESAuthority keeps HTTP origin checks in the Search client while sharing
// the same opt-in network policy as RPC. Arbitrary DNS names remain prohibited.
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
