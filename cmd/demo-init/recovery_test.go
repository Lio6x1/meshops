package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"example.com/meshops-course/internal/search"
)

func completeMarker() search.Bootstrap {
	return search.Bootstrap{Version: 1, Complete: true, Database: "meshops_course", Index: search.TaskIndexName, IndexUUID: "old-index", Position: search.BinlogPosition{File: "meshops-bin.000001", Offset: 4}}
}

func TestSearchRebuildPublishesOnlyCompleteValidatedMarker(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "interrupted"}[fail], func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "search-bootstrap.json")
			if err := search.SaveBootstrap(path, completeMarker()); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "unrelated-fact"), []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			err := rebuildSearchMarker(context.Background(), dir, func(context.Context) error {
				if _, err := search.LoadBootstrap(path); err == nil {
					t.Fatal("old complete marker remained published during rebuild")
				}
				b := completeMarker()
				b.IndexUUID = "new-index"
				if err := search.SaveBootstrap(path, b); err != nil {
					return err
				}
				if fail {
					return errors.New("injected post-write failure")
				}
				return nil
			})
			if fail {
				if err == nil {
					t.Fatal("failure hidden")
				}
				if _, err := search.LoadBootstrap(path); err == nil {
					t.Fatal("failed rebuild left ready marker")
				}
			} else {
				b, e := search.LoadBootstrap(path)
				if err != nil || e != nil || b.IndexUUID != "new-index" {
					t.Fatal(b, err, e)
				}
			}
			got, _ := os.ReadFile(filepath.Join(dir, "unrelated-fact"))
			if string(got) != "keep" {
				t.Fatal("unrelated state changed")
			}
		})
	}
}

func TestCanalResetRemovesOnlyValidatedPositionAndTSDBFiles(t *testing.T) {
	base := t.TempDir()
	destination := filepath.Join(base, "meshops")
	if err := os.Mkdir(destination, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"meta.dat", "h2.mv.db", "h2.trace.db", "h2.lock.db"} {
		if err := os.WriteFile(filepath.Join(destination, name), []byte("old"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	other := filepath.Join(base, "other-destination")
	if err := os.WriteFile(other, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := resetCanalMeta(base); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("expected empty destination removed", err)
	}
	if err := resetCanalMeta(base); err != nil {
		t.Fatal("idempotent retry", err)
	}
	got, _ := os.ReadFile(other)
	if string(got) != "keep" {
		t.Fatal("other destination changed")
	}
}

func TestCanalResetRejectsUnknownFilesOrDirectoriesBeforeDeleting(t *testing.T) {
	for _, directory := range []bool{false, true} {
		t.Run(map[bool]string{false: "unknown", true: "directory"}[directory], func(t *testing.T) {
			base := t.TempDir()
			destination := filepath.Join(base, "meshops")
			if err := os.Mkdir(destination, 0700); err != nil {
				t.Fatal(err)
			}
			known := filepath.Join(destination, "meta.dat")
			if err := os.WriteFile(known, []byte("keep"), 0600); err != nil {
				t.Fatal(err)
			}
			if directory {
				if err := os.Mkdir(filepath.Join(destination, "h2.mv.db"), 0700); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.WriteFile(filepath.Join(destination, "unexpected"), []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := resetCanalMeta(base); err == nil {
				t.Fatal("unexpected metadata entry accepted")
			}
			got, _ := os.ReadFile(known)
			if string(got) != "keep" {
				t.Fatal("partially deleted before validating all entries")
			}
		})
	}
}
