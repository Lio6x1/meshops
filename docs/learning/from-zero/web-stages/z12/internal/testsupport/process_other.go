//go:build !windows

package testsupport

import "os/exec"

func Hide(cmd *exec.Cmd) {}
