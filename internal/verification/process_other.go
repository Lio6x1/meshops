//go:build !windows

package verification

import "os/exec"

func hide(c *exec.Cmd) {}
