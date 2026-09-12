package main

import (
	"context"
	commonv1 "example.com/meshops-course/gen/common/v1"
	entityv1 "example.com/meshops-course/gen/entity/v1"
	ingestv1 "example.com/meshops-course/gen/ingest/v1"
	"example.com/meshops-course/internal/platform"
	"example.com/meshops-course/internal/state"
	"flag"
	"fmt"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"io"
	"os"
	"time"
)

func printProto(v proto.Message) error {
	b, e := protojson.MarshalOptions{Multiline: true, EmitUnpopulated: true}.Marshal(v)
	if e == nil {
		fmt.Println(string(b))
	}
	return e
}
func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("usage: course serve|get|report|normalize|watch")
	}
	mode := os.Args[1]
	f := flag.NewFlagSet(mode, flag.ContinueOnError)
	config := f.String("f", "configs/server.yaml", "server YAML")
	role := f.String("role", "all", "all in Z04; ingest or entity in Z05/Z06")
	manifest := f.String("manifest", "configs/simulation.yaml", "trusted bindings")
	endpoint := f.String("endpoint", "127.0.0.1:25151", "RPC endpoint")
	id := f.String("id", "person-001", "entity/raw ID")
	sourceID := f.String("source", "personnel_sim", "registered source")
	version := f.Int64("version", 1, "entity version; manual only before durable gateway")
	rawPath := f.String("raw", "", "read an exact raw fixture instead of generating a fresh event")
	tokenEnv := f.String("token-env", "", "override credential from environment")
	if e := f.Parse(os.Args[2:]); e != nil {
		return e
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	if mode == "serve" {
		return serve(*config, *role)
	}
	r, e := platform.LoadRegistry(*manifest)
	if e != nil {
		return e
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if mode == "get" || mode == "watch" {
		token, e := r.Credential("demo_tenant", "demo_operator")
		if e != nil {
			return e
		}
		if *tokenEnv != "" {
			token = os.Getenv(*tokenEnv)
		}
		conn, e := platform.Dial(*endpoint)
		if e != nil {
			return e
		}
		defer conn.Close()
		client := entityv1.NewEntityServiceClient(conn)
		ctx = platform.Outgoing(ctx, token)
		if mode == "watch" {
			s, e := client.Subscribe(ctx, &entityv1.SubscribeRequest{EntityIds: []string{*id}})
			if e != nil {
				return e
			}
			for {
				v, e := s.Recv()
				if e == io.EOF {
					return nil
				}
				if e != nil {
					return e
				}
				if e = printProto(v); e != nil {
					return e
				}
			}
		}
		v, e := client.GetSnapshot(ctx, &entityv1.GetSnapshotRequest{EntityId: *id})
		if e != nil {
			return e
		}
		return printProto(v)
	}
	if mode != "report" && mode != "normalize" {
		return fmt.Errorf("unknown command %s", mode)
	}
	source, ok := r.GetSource("demo_tenant", *sourceID)
	if !ok {
		return fmt.Errorf("unknown source")
	}
	now := time.Now().UTC()
	var raw []byte
	if *rawPath != "" {
		raw, e = os.ReadFile(*rawPath)
	} else {
		raw, e = state.GenerateRaw(source, *id, *version, now)
	}
	if e != nil {
		return e
	}
	event, e := state.Normalize(raw, source, now)
	if e != nil {
		return e
	}
	if mode == "normalize" {
		return printProto(event)
	}
	token, e := r.Credential(source.TenantID, source.ID)
	if e != nil {
		return e
	}
	if *tokenEnv != "" {
		token = os.Getenv(*tokenEnv)
	}
	conn, e := platform.Dial(*endpoint)
	if e != nil {
		return e
	}
	defer conn.Close()
	s, e := ingestv1.NewIngestServiceClient(conn).ReportEntityStates(platform.Outgoing(ctx, token))
	if e != nil {
		return e
	}
	if e = s.Send(&ingestv1.ReportEntityStatesRequest{GatewayEpoch: platform.NewID(), FirstSequence: 1, Events: []*commonv1.EntityStateEvent{event}}); e != nil {
		return e
	}
	ack, e := s.Recv()
	if e != nil {
		return e
	}
	if e = printProto(ack); e != nil {
		return e
	}
	if len(ack.Errors) > 0 || ack.ConfirmedSequence != 1 {
		return fmt.Errorf("event was not acknowledged")
	}
	if e = s.CloseSend(); e != nil {
		return e
	}
	_, e = s.Recv()
	if e != io.EOF {
		return fmt.Errorf("stream did not close cleanly: %v", e)
	}
	return nil
}
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
