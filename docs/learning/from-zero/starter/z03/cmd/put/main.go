package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	commonv1 "example.com/meshops-course/gen/common/v1"
	entityv1 "example.com/meshops-course/gen/entity/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

func run() error {
	addr := flag.String("addr", "127.0.0.1:50163", "server address")
	id := flag.String("id", "person-001", "entity ID")
	kind := flag.String("type", "person", "person or drone")
	lat := flag.Float64("lat", 31.2304, "latitude in degrees")
	lon := flag.Float64("lon", 121.4737, "longitude in degrees")
	battery := flag.String("battery", "", "battery percent; omitted means unknown")
	flag.Parse()
	row := &commonv1.EntitySnapshot{EntityType: *kind, Location: &commonv1.Location{Latitude: *lat, Longitude: *lon}}
	if *battery != "" {
		value, err := strconv.ParseFloat(*battery, 64)
		if err != nil {
			return fmt.Errorf("battery must be a number: %w", err)
		}
		row.Power = &commonv1.PowerState{BatteryPercent: &value}
	}
	conn, err := grpc.NewClient(*addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()
	client := entityv1.NewEntityServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	reply, err := client.PutSnapshot(ctx, &entityv1.PutSnapshotRequest{EntityId: *id, Snapshot: row})
	if err != nil {
		return err
	}
	fmt.Printf("id=%s stored=memory-only\n", reply.GetEntityId())
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "put failed: code=%s message=%s\n", status.Code(err), status.Convert(err).Message())
		os.Exit(1)
	}
}
