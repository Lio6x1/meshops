package testsupport

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Barrier is reached only after the test child has performed the specified
// durable operation. The parent uses Process.Kill, so no deferred Close runs.
func Barrier(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(os.Getenv("MESHOPS_CRASH_DIR"), "barrier"), []byte("durable boundary reached"), 0600); err != nil {
		t.Fatal(err)
	}
	for {
		time.Sleep(time.Hour)
	}
}
func KillAtBarrier(t *testing.T, dir, mode string, env ...string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, "-test.run=^TestDurableCrashChild$", "-test.v")
	Hide(cmd)
	cmd.Env = append(os.Environ(), "MESHOPS_CRASH_MODE="+mode, "MESHOPS_CRASH_DIR="+dir, "MESHOPS_TEST_MYSQL_ADMIN_DSN=")
	cmd.Env = append(cmd.Env, env...)
	log, err := os.Create(filepath.Join(dir, "child.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd.Stdout = log
	cmd.Stderr = log
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	reaped := false
	defer func() {
		if !reaped {
			cmd.Process.Kill()
			<-done
		}
	}()
	deadline := time.Now().Add(45 * time.Second)
	for {
		if _, err = os.Stat(filepath.Join(dir, "barrier")); err == nil {
			break
		}
		select {
		case err := <-done:
			reaped = true
			raw, _ := os.ReadFile(filepath.Join(dir, "child.log"))
			t.Fatalf("child exited before durable barrier: %v %s", err, raw)
		default:
		}
		if time.Now().After(deadline) {
			raw, _ := os.ReadFile(filepath.Join(dir, "child.log"))
			t.Fatalf("child never reached durable barrier: %s", raw)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case err = <-done:
		reaped = true
		if err == nil {
			t.Fatal("child exited normally instead of being killed")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("killed child did not exit")
	}
	t.Log("force-killed child at durable barrier", mode)
}
