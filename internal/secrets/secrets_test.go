package secrets

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTool writes a shell script that runs body with /bin/sh and returns
// its path. The script sees DUMP as the path of an evidence file.
func writeTool(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tool.sh")
	script := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func readDump(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestFetchCapturesStdout(t *testing.T) {
	bin := writeTool(t, `printf 's3cr3t-value'`)
	got, err := Fetch(Tool{Name: "fake", Bin: bin})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got != "s3cr3t-value" {
		t.Fatalf("got %q", got)
	}
}

func TestFetchTrimsTrailingNewline(t *testing.T) {
	bin := writeTool(t, `printf 's3cr3t-value\n\n\r\n'`)
	got, err := Fetch(Tool{Name: "fake", Bin: bin})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if got != "s3cr3t-value" {
		t.Fatalf("got %q", got)
	}
}

func TestFetchEnvIsExplicitOnly(t *testing.T) {
	dump := filepath.Join(t.TempDir(), "env.txt")
	bin := writeTool(t, `printf v; env > "$DUMP"`)
	t.Setenv("DM_SECRETS_CANARY", "inherited")
	tool := Tool{
		Name: "fake",
		Bin:  bin,
		Args: []string{"fetch", "item"},
		Env:  []string{"FAKE_CONFIG=required-by-tool", "DUMP=" + dump},
	}
	if _, err := Fetch(tool); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	env := readDump(t, dump)
	if !strings.Contains(env, "FAKE_CONFIG=required-by-tool") {
		t.Fatalf("explicit config env missing:\n%s", env)
	}
	if strings.Contains(env, "DM_SECRETS_CANARY") {
		t.Fatalf("parent env leaked to child:\n%s", env)
	}
}

func TestFetchEmptyEnvPassesNothing(t *testing.T) {
	dump := filepath.Join(t.TempDir(), "env.txt")
	bin := writeTool(t, `printf v; env > "$DUMP"`)
	t.Setenv("DM_SECRETS_CANARY", "inherited")
	tool := Tool{Name: "fake", Bin: bin, Env: []string{"DUMP=" + dump}}
	if _, err := Fetch(tool); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	env := readDump(t, dump)
	if strings.Contains(env, "DM_SECRETS_CANARY") {
		t.Fatalf("parent env leaked to child with empty Tool.Env:\n%s", env)
	}
	for line := range strings.SplitSeq(env, "\n") {
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "DUMP="), strings.HasPrefix(line, "PWD="),
			strings.HasPrefix(line, "SHLVL="), strings.HasPrefix(line, "_="):
		default:
			t.Fatalf("unexpected child env entry from parent: %q\nfull:\n%s", line, env)
		}
	}
}

func TestFetchValueNeverReachesArgv(t *testing.T) {
	dump := filepath.Join(t.TempDir(), "argv.txt")
	bin := writeTool(t, `printf 's3cr3t-value'; printf '%s\n' "$@" > "$DUMP"`)
	tool := Tool{Name: "fake", Bin: bin, Args: []string{"read", "op://vault/item"}, Env: []string{"DUMP=" + dump}}
	if _, err := Fetch(tool); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	argv := readDump(t, dump)
	if argv != "read\nop://vault/item\n" {
		t.Fatalf("unexpected argv: %q", argv)
	}
	if strings.Contains(argv, "s3cr3t-value") {
		t.Fatalf("value reached argv: %q", argv)
	}
}

func TestFetchErrorExcludesValueAndToolOutput(t *testing.T) {
	bin := writeTool(t, `printf 's3cr3t-value'; echo 'boom s3cr3t-value' >&2; exit 3`)
	_, err := Fetch(Tool{Name: "fake", Bin: bin})
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !strings.Contains(err.Error(), "exited 3") {
		t.Fatalf("error lacks exit code: %v", err)
	}
	if strings.Contains(err.Error(), "s3cr3t-value") {
		t.Fatalf("value leaked into error: %v", err)
	}
}

func TestFetchMissingBinary(t *testing.T) {
	_, err := Fetch(Tool{Name: "fake"})
	if err == nil || !strings.Contains(err.Error(), "no binary") {
		t.Fatalf("want no-binary error, got %v", err)
	}
}

func TestRegistryPutGetDropReplace(t *testing.T) {
	r := NewRegistry()
	if _, ok := r.Get("db"); ok {
		t.Fatal("empty registry returned a value")
	}
	r.Put("db", "first")
	if v, ok := r.Get("db"); !ok || v != "first" {
		t.Fatalf("Get after Put: %q %v", v, ok)
	}
	if !r.Drop("db") {
		t.Fatal("Drop after Put: want true")
	}
	if r.Drop("db") {
		t.Fatal("second Drop: want false")
	}
	r.Put("db", "rotated")
	if v, ok := r.Get("db"); !ok || v != "rotated" {
		t.Fatalf("Get after replace: %q %v", v, ok)
	}
}

func TestHydrateCreatesParentsAndPerms(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "deep", "nested", "secret-file")
	if err := Hydrate(path, "s3cr3t-value"); err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("file perm: %o", fi.Mode().Perm())
	}
	di, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if di.Mode().Perm() != 0o700 {
		t.Fatalf("parent perm: %o", di.Mode().Perm())
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "s3cr3t-value" {
		t.Fatalf("content: %q", b)
	}
}

func TestHydrateTightensExistingPerms(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "secret-file")
	if err := os.WriteFile(path, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Hydrate(path, "s3cr3t-value"); err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("file perm after hydrate: %o", fi.Mode().Perm())
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "s3cr3t-value" {
		t.Fatalf("content: %q", b)
	}
}

func TestHydrateReplacesPlantedSymlink(t *testing.T) {
	root := t.TempDir()
	victim := filepath.Join(root, "victim")
	if err := os.WriteFile(victim, []byte("victim-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "target")
	if err := os.Symlink(victim, target); err != nil {
		t.Fatal(err)
	}
	if err := Hydrate(target, "s3cr3t-value"); err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	b, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "victim-data" {
		t.Fatalf("symlink was followed, victim now holds %q", b)
	}
	fi, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Fatal("target is still a symlink")
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("target perm: %o", fi.Mode().Perm())
	}
	b, err = os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "s3cr3t-value" {
		t.Fatalf("target content: %q", b)
	}
}

func TestHydrateErrorExcludesValue(t *testing.T) {
	root := t.TempDir()
	blocker := filepath.Join(root, "blocker")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(blocker, "child", "secret-file")
	err := Hydrate(path, "s3cr3t-value")
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if strings.Contains(err.Error(), "s3cr3t-value") {
		t.Fatalf("value leaked into error: %v", err)
	}
}

func TestFetchThenRegistryThenHydrate(t *testing.T) {
	bin := writeTool(t, `printf 's3cr3t-value'`)
	v, err := Fetch(Tool{Name: "fake", Bin: bin})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	r := NewRegistry()
	r.Put("db", v)
	got, ok := r.Get("db")
	if !ok || got != v {
		t.Fatalf("registry round trip: %q %v", got, ok)
	}
	path := filepath.Join(t.TempDir(), "out", "db-creds")
	if err := Hydrate(path, got); err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "s3cr3t-value" {
		t.Fatalf("hydrated content: %q", b)
	}
}

// TestFetchInheritsNamedVars proves the child env holds exactly the names
// Inherit lists from the parent plus the explicit entries, and that an
// explicit entry wins over an inherited one.
func TestFetchInheritsNamedVars(t *testing.T) {
	dump := filepath.Join(t.TempDir(), "env.txt")
	bin := writeTool(t, `env > "$DUMP"`)
	t.Setenv("DM_SECRETS_CANARY", "inherited")
	t.Setenv("DM_SECRETS_OVERRIDE", "from-parent")
	tool := Tool{
		Name: "fake", Bin: bin,
		Inherit: []string{"DM_SECRETS_CANARY", "DM_SECRETS_OVERRIDE"},
		Env:     []string{"DUMP=" + dump, "DM_SECRETS_OVERRIDE=explicit"},
	}
	if _, err := Fetch(tool); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	env := readDump(t, dump)
	if !strings.Contains(env, "DM_SECRETS_CANARY=inherited") {
		t.Fatalf("the inherited name did not reach the child:\n%s", env)
	}
	if strings.Contains(env, "DM_SECRETS_OVERRIDE=from-parent") {
		t.Fatalf("the explicit entry did not win:\n%s", env)
	}
	if !strings.Contains(env, "DM_SECRETS_OVERRIDE=explicit") {
		t.Fatalf("the explicit entry is missing:\n%s", env)
	}
	if strings.Contains(env, "HOME=") {
		t.Fatalf("an uninherited name reached the child:\n%s", env)
	}
}
