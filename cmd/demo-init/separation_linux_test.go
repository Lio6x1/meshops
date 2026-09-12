//go:build linux

package main

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestLinuxDerivedCredentialModesAndRootProvisionedOwners(t *testing.T) {
	dir := t.TempDir()
	if _, err := ensureSecrets(dir); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		name     string
		uid, gid uint32
		mode     os.FileMode
	}{
		{"secrets.json", 0, 0, 0600},
		{"runtime-secrets.json", 10001, 10001, 0440},
		{"web-codes.json", 10002, 10001, 0400},
		{"mysql-root-password", 999, 999, 0400},
	} {
		info, err := os.Stat(filepath.Join(dir, item.name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != item.mode {
			t.Errorf("%s mode %o want %o", item.name, info.Mode().Perm(), item.mode)
		}
		// CI may run unprivileged; UID assignment itself is additionally checked
		// when this same test runs as the container's root provisioning account.
		if os.Geteuid() == 0 {
			s := info.Sys().(*syscall.Stat_t)
			if s.Uid != item.uid || s.Gid != item.gid {
				t.Errorf("%s owner %d:%d want %d:%d", item.name, s.Uid, s.Gid, item.uid, item.gid)
			}
		}
	}
}

func TestCanalResetRejectsSymlinkWithoutTouchingTarget(t *testing.T) {
	base := t.TempDir()
	destination := filepath.Join(base, "meshops")
	if err := os.Mkdir(destination, 0700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "keep")
	if err := os.WriteFile(outside, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(destination, "meta.dat")); err != nil {
		t.Fatal(err)
	}
	if err := resetCanalMeta(base); err == nil {
		t.Fatal("metadata symlink accepted")
	}
	got, _ := os.ReadFile(outside)
	if string(got) != "keep" {
		t.Fatal("symlink target changed")
	}
}
