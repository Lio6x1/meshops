package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"example.com/meshops-course/internal/search"
)

func invalidateSearch(dir string) error {
	path := filepath.Join(dir, "search-bootstrap.json")
	if err := search.SaveBootstrap(path, search.Bootstrap{Version: 1, Database: "meshops_course", Index: search.TaskIndexName}); err != nil {
		return err
	}
	return ownSecret(path)
}

// The orchestration owns service shutdown. This helper owns the durable marker:
// any unsuccessful import (even after a child wrote complete=true) invalidates
// it again, so a later ordinary Up cannot silently serve a partial projection.
func rebuildSearchMarker(ctx context.Context, dir string, importSnapshot func(context.Context) error) (err error) {
	if err = invalidateSearch(dir); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, invalidateSearch(dir))
		}
	}()
	if err = importSnapshot(ctx); err != nil {
		return fmt.Errorf("search rebuild failed: %w", err)
	}
	if _, err = search.LoadBootstrap(filepath.Join(dir, "search-bootstrap.json")); err != nil {
		return err
	}
	return ownSecret(filepath.Join(dir, "search-bootstrap.json"))
}

// Production passes one fixed mount path; no user-provided deletion path or
// recursive delete is accepted. These are the files observed for the configured
// CanalFileMetaManager and its H2 TSDB. Validate the entire directory before any
// deletion, and reject new formats so maintenance cannot silently miss a cursor.
func resetCanalMeta(base string) error {
	info, err := os.Lstat(base)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("canal metadata mount is not a real directory")
	}
	destination := filepath.Join(base, "meshops")
	info, err = os.Lstat(destination)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("canal destination is not a real directory")
	}
	entries, err := os.ReadDir(destination)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		switch entry.Name() {
		case "meta.dat", "h2.mv.db", "h2.trace.db", "h2.lock.db":
		default:
			return fmt.Errorf("unknown Canal metadata file %s; inspect before recovery", entry.Name())
		}
		info, e := os.Lstat(filepath.Join(destination, entry.Name()))
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			return errors.New("canal metadata contains a directory or symbolic link")
		}
	}
	for _, entry := range entries {
		if err = os.Remove(filepath.Join(destination, entry.Name())); err != nil {
			return err
		}
	}
	// Remove only an empty, validated directory; never RemoveAll a volume.
	return os.Remove(destination)
}
