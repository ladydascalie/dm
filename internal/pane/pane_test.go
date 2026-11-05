package pane

import (
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestShellCwd reads a process's directory through /proc and survives a pid
// that has none.
func TestShellCwd(t *testing.T) {
	dir := t.TempDir()
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", "cd "+want+"; sleep 10")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	deadline := time.Now().Add(5 * time.Second)
	for shellCwd(cmd.Process.Pid) != want {
		if time.Now().After(deadline) {
			t.Fatalf("shellCwd(%d) never reached %q", cmd.Process.Pid, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := shellCwd(-1); got != "" {
		t.Fatalf("shellCwd(-1) = %q, want empty", got)
	}
}
