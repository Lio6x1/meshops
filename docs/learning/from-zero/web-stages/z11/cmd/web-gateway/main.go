// web-gateway holds browser sessions and reuses authenticated internal RPCs.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	dispatcherv1 "example.com/meshops-course/gen/dispatcher/v1"
	entityv1 "example.com/meshops-course/gen/entity/v1"
	searchv1 "example.com/meshops-course/gen/search/v1"
	taskv1 "example.com/meshops-course/gen/task/v1"
	"example.com/meshops-course/internal/platform"
	"example.com/meshops-course/internal/simulation"
	"example.com/meshops-course/internal/web"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
)

func env(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
func main() {
	if err := run(); err != nil {
		slog.Error("web gateway stopped", "error", err)
		os.Exit(1)
	}
}
func newHTTPServer(ctx context.Context, handler http.Handler) *http.Server {
	// ReadTimeout also bounds partial JSON bodies. A context timeout only bounds
	// RPC work; it cannot unblock net/http's Body.Read on a slow client socket.
	// Leave the global WriteTimeout unset: each SSE write has its own deadline.
	return &http.Server{Handler: handler, ReadTimeout: 10 * time.Second, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10, BaseContext: func(net.Listener) context.Context { return ctx }}
}
func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	reg, err := platform.LoadRegistry(env("MESHOPS_MANIFEST", "configs/simulation.yaml"))
	if err != nil {
		return err
	}
	var conns []*grpc.ClientConn
	defer func() {
		for _, c := range conns {
			_ = c.Close()
		}
	}()
	for _, v := range []struct{ name, def string }{{"MESHOPS_ENTITY_ENDPOINT", "127.0.0.1:50052"}, {"MESHOPS_TASK_ENDPOINT", "127.0.0.1:50053"}, {"MESHOPS_DISPATCHER_ENDPOINT", "127.0.0.1:50054"}, {"MESHOPS_SEARCH_ENDPOINT", "127.0.0.1:50055"}} {
		c, e := platform.Dial(env(v.name, v.def))
		if e != nil {
			return e
		}
		conns = append(conns, c)
	}
	var control web.SimulationControl
	if os.Getenv("MESHOPS_SIMULATION_CONTROL") == "1" {
		client := redis.NewClient(&redis.Options{Addr: env("MESHOPS_REDIS_ADDR", "127.0.0.1:16379"), DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second, MaxRetries: 0})
		defer client.Close()
		control = simulation.NewStore(client)
	}
	app, err := web.New(web.Config{Simulation: control, Registry: reg, TenantID: env("MESHOPS_WEB_TENANT", "demo_tenant"), Entity: entityv1.NewEntityServiceClient(conns[0]), Task: taskv1.NewTaskServiceClient(conns[1]), Dispatcher: dispatcherv1.NewDispatcherServiceClient(conns[2]), Search: searchv1.NewSearchServiceClient(conns[3]), AccessCodes: map[string]string{"operator": os.Getenv("MESHOPS_WEB_OPERATOR_CODE"), "admin": os.Getenv("MESHOPS_WEB_ADMIN_CODE")}, AllowedOrigins: strings.Split(env("MESHOPS_WEB_ORIGINS", "http://localhost:18090,http://127.0.0.1:18090,http://localhost:5173,http://127.0.0.1:5173"), ",")})
	if err != nil {
		return err
	}
	address := env("MESHOPS_WEB_LISTEN", "127.0.0.1:18090")
	host, _, err := net.SplitHostPort(address)
	ip := net.ParseIP(host)
	if err != nil || ip == nil || (!ip.IsLoopback() && !(os.Getenv("MESHOPS_NETWORK_MODE") == "compose-demo" && address == "0.0.0.0:18090")) {
		return errors.New("web listener requires loopback or explicit isolated compose-demo listener")
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	server := newHTTPServer(ctx, app.Handler())
	failures := make(chan error, 1)
	go func() { failures <- server.Serve(listener) }()
	slog.Info("browser gateway listening", "address", address)
	select {
	case err = <-failures:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	case <-ctx.Done():
	}
	drain, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err = server.Shutdown(drain); err != nil {
		_ = server.Close()
		return err
	}
	err = <-failures
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
