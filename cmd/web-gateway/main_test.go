package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"
)

// A context deadline alone does not interrupt an incomplete HTTP request body.
// Use a real socket and deliberately withhold JSON bytes to verify that the
// server's transport deadline releases the blocked handler.
func TestSlowRequestBodyIsBounded(t *testing.T) {
	finished := make(chan error, 1)
	server := newHTTPServer(context.Background(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		err := json.NewDecoder(r.Body).Decode(&body)
		finished <- err
		if err != nil {
			http.Error(w, "invalid body", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	if server.ReadTimeout > 0 {
		server.ReadTimeout = 100 * time.Millisecond
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	_, err = fmt.Fprint(conn, "POST /api/session HTTP/1.1\r\nHost: localhost:18090\r\nContent-Length: 100\r\nContent-Type: application/json\r\n\r\n{\"role\":")
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatalf("slow request was not rejected within the transport bound: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d", response.StatusCode)
	}
	select {
	case err = <-finished:
		if err == nil {
			t.Fatal("incomplete body was accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("request body handler remained blocked")
	}
}
