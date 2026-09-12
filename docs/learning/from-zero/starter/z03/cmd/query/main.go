package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	entityv1 "example.com/meshops-course/gen/entity/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

func run() error {
	addr := flag.String("addr", "127.0.0.1:50163", "server address")
	id := flag.String("id", "person-001", "entity ID")
	flag.Parse()
	conn, err := grpc.NewClient(*addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()
	client := entityv1.NewEntityServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	reply, err := client.GetSnapshot(ctx, &entityv1.GetSnapshotRequest{EntityId: *id})
	if err != nil {
		return err
	}
	if !reply.GetFound() {
		fmt.Printf("id=%s found=false\n", reply.GetEntityId())
		return nil
	}
	snapshot := reply.GetSnapshot()
	if snapshot == nil || snapshot.GetLocation() == nil {
		return fmt.Errorf("response has no snapshot/location")
	}
	power := "unknown"
	if snapshot.Power != nil && snapshot.Power.BatteryPercent != nil {
		power = fmt.Sprintf("%.1f%%", *snapshot.Power.BatteryPercent)
	}
	fmt.Printf("id=%s found=true type=%s lat=%.4f lon=%.4f power=%s\n",
		reply.GetEntityId(), snapshot.GetEntityType(), snapshot.Location.Latitude, snapshot.Location.Longitude, power)
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "query failed: code=%s message=%s\n", status.Code(err), status.Convert(err).Message())
		os.Exit(1)
	}
}
