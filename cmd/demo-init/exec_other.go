//go:build !linux

package main

import (
	"errors"
	"os"
)

func replaceProcess([]string) error {
	return errors.New("demo exec wrapper runs inside Linux containers only")
}
func ownSecret(string) error                                    { return nil }
func protectFile(path string, _, _ int, mode os.FileMode) error { return os.Chmod(path, mode) }
