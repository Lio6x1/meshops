package platform

import "testing"

func TestComposeDemoRPCPolicy(t *testing.T) {
	t.Setenv("MESHOPS_NETWORK_MODE", "compose-demo")
	for _, endpoint := range []string{"ingest:50051", "entity:50052", "task:50053", "dispatcher:50054", "search:50055", "127.0.0.1:50052"} {
		conn, err := Dial(endpoint)
		if err != nil {
			t.Errorf("allowed service %s: %v", endpoint, err)
			continue
		}
		conn.Close()
	}
	for _, endpoint := range []string{"entity:1234", "entity.example:50052", "dns:///entity:50052", "passthrough:///entity:50052", "ENTITY:50052", "192.0.2.1:50052", "0.0.0.0:50052"} {
		conn, err := Dial(endpoint)
		if conn != nil {
			conn.Close()
		}
		if err == nil {
			t.Errorf("accepted unexpected destination %s", endpoint)
		}
	}
	if err := localRPCAddress("0.0.0.0:50052", true); err != nil {
		t.Fatal("demo listener rejected:", err)
	}
	if err := localRPCAddress("0.0.0.0:9999", true); err == nil {
		t.Fatal("unknown listener port accepted")
	}
}

func TestUnknownNetworkModeFailsClosed(t *testing.T) {
	t.Setenv("MESHOPS_NETWORK_MODE", "true")
	conn, err := Dial("127.0.0.1:50052")
	if conn != nil {
		conn.Close()
	}
	if err == nil {
		t.Fatal("unknown mode silently accepted")
	}
}
