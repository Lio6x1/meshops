//go:build linux

package main

import (
	"os"
	"syscall"
)

// Replace the wrapper so the actual server receives Docker's SIGTERM as PID1.
func replaceProcess(args []string) error { return syscall.Exec(args[0], args, os.Environ()) }
func ownSecret(path string) error {
	if os.Geteuid() == 0 {
		return os.Chown(path, 10001, 10001)
	}
	return nil
}

func protectFile(path string, uid, gid int, mode os.FileMode) error {
	if os.Geteuid() == 0 {
		if err := os.Chown(path, uid, gid); err != nil {
			return err
		}
	}
	return os.Chmod(path, mode)
}
