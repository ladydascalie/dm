package pane

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestShellCwd reads a process's directory through /proc and survives a pid
// that has none.
func TestShellCwd(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", "cd "+want+"; sleep 10")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill() }()
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

// TestParseArgvRejectsBadElements pins the --argv guard rails: an empty
// element and a NUL byte are refused with an error naming the element, before
// Run touches a pty or a socket.
func TestParseArgvRejectsBadElements(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		argv string
		want string
	}{
		{"middle empty", "htop,,sort", "empty"},
		{"leading comma", ",htop", "empty"},
		{"trailing comma", "htop,", "empty"},
		{"nul byte", "htop\x00x", "NUL"},
		{"bare nul", "\x00", "NUL"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := parsePaneArgs([]string{"--id", "p1", "--session", "s1", "--argv", tc.argv})
			if err == nil {
				t.Fatalf("--argv %q accepted", tc.argv)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("--argv %q error = %q, want it to name %s", tc.argv, err.Error(), tc.want)
			}
		})
	}
}

// TestParseArgvAcceptsOneProgram pins the happy flag shape: a program with
// arguments parses to one element per comma, and an absent flag parses to
// nothing, so the pane runs its shell.
func TestParseArgvAcceptsOneProgram(t *testing.T) {
	t.Parallel()
	o, err := parsePaneArgs([]string{"--id", "p1", "--session", "s1",
		"--shell", "/bin/sh", "--argv", "htop,-d,5"})
	if err != nil {
		t.Fatalf("parsePaneArgs: %v", err)
	}
	if len(o.argv) != 3 || o.argv[0] != "htop" || o.argv[1] != "-d" || o.argv[2] != "5" {
		t.Fatalf("argv = %v, want [htop -d 5]", o.argv)
	}
	o, err = parsePaneArgs([]string{"--id", "p2", "--session", "s1", "--shell", "/bin/sh"})
	if err != nil {
		t.Fatalf("parsePaneArgs without argv: %v", err)
	}
	if len(o.argv) != 0 {
		t.Fatalf("argv = %v, want none", o.argv)
	}
}

// TestPaneCmdRunsProgramAndExportsSession proves a pane born with --argv runs
// that program instead of $SHELL -l, and both forms export DM_PANE and
// DM_SESSION so the program still knows its own session.
func TestPaneCmdRunsProgramAndExportsSession(t *testing.T) {
	t.Parallel()
	o, err := parsePaneArgs([]string{"--id", "w1p1", "--session", "main",
		"--cwd", "/tmp", "--shell", "/bin/sh", "--argv", "htop,arg one"})
	if err != nil {
		t.Fatalf("parsePaneArgs: %v", err)
	}
	cmd := paneCmd(o)
	if got, want := strings.Join(cmd.Args, " "), "htop arg one"; got != want {
		t.Fatalf("command = %q, want %q", got, want)
	}
	if !hasPaneEnv(cmd.Env, "DM_PANE", "w1p1") || !hasPaneEnv(cmd.Env, "DM_SESSION", "main") {
		t.Fatalf("argv pane env = %v, want DM_PANE and DM_SESSION exported", cmd.Env)
	}
	if cmd.Dir != "/tmp" {
		t.Fatalf("argv pane dir = %q, want /tmp", cmd.Dir)
	}

	o, err = parsePaneArgs([]string{"--id", "w1p2", "--session", "main", "--shell", "/bin/zsh"})
	if err != nil {
		t.Fatalf("parsePaneArgs shell pane: %v", err)
	}
	cmd = paneCmd(o)
	if got, want := strings.Join(cmd.Args, " "), "/bin/zsh -l"; got != want {
		t.Fatalf("shell command = %q, want %q", got, want)
	}
	if !hasPaneEnv(cmd.Env, "DM_PANE", "w1p2") || !hasPaneEnv(cmd.Env, "DM_SESSION", "main") {
		t.Fatalf("shell pane env = %v, want DM_PANE and DM_SESSION exported", cmd.Env)
	}
}

// hasPaneEnv reports whether vars holds name=value.
func hasPaneEnv(vars []string, name, value string) bool {
	for _, v := range vars {
		if v == name+"="+value {
			return true
		}
	}
	return false
}
