package search

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBootstrapMarkerRequiresCompletionAndIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bootstrap.json")
	m := Bootstrap{Version: 1, Database: "meshops_course", Index: "meshops-tasks-v1", IndexUUID: "uuid-1", Position: BinlogPosition{File: "binlog.000001", Offset: 123}}
	if err := SaveBootstrap(path, m); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadBootstrap(path); err == nil {
		t.Fatal("incomplete bootstrap accepted")
	}
	m.Complete = true
	if err := SaveBootstrap(path, m); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadBootstrap(path)
	if err != nil || loaded.IndexUUID != "uuid-1" {
		t.Fatalf("marker %v %v", loaded, err)
	}
	m.CDCStart = -1
	if err := SaveBootstrap(path, m); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadBootstrap(path); err == nil {
		t.Fatal("negative CDC boundary accepted")
	}
	if err := os.WriteFile(path, []byte(`{"version":1,"complete":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadBootstrap(path); err == nil {
		t.Fatal("missing identity accepted")
	}
}
