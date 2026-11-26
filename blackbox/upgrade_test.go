package blackbox

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestVersion pins the two shapes dm version prints. A test build carries no
// linker stamp, so Version and Commit hold their dev defaults: the dev text is
// the shape under test, not a stamped release.
func TestVersion(t *testing.T) {
	bin := buildOnce(t)
	env := testEnv(os.Environ())

	text, err := capture(bin, env, "version")
	if err != nil {
		t.Fatalf("dm version: %v\n%s", err, text)
	}
	want := "dm dev (" + runtime.GOOS + "/" + runtime.GOARCH + ")\n"
	if text != want {
		t.Fatalf("dm version = %q, want %q", text, want)
	}

	raw, err := capture(bin, env, "version", "--json")
	if err != nil {
		t.Fatalf("dm version --json: %v\n%s", err, raw)
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(raw), &fields); err != nil {
		t.Fatalf("dm version --json printed %q, not one object: %v", raw, err)
	}
	wantJSON := map[string]any{
		"version": "dev",
		"commit":  "none",
		"os":      runtime.GOOS,
		"arch":    runtime.GOARCH,
	}
	for key, value := range wantJSON {
		if got := fields[key]; got != value {
			t.Errorf("version --json %s = %v, want %v", key, got, value)
		}
	}
	if len(fields) != len(wantJSON) {
		t.Fatalf("version --json = %s, want the four keys only, no wrapper", raw)
	}
}

// TestUpgrade drives the real binary through a fake release: a stamped tarball
// behind DM_RELEASE_API, an install reached through a relative symlink, and the
// refusals that must leave the old binary alone. No session and no pty: neither
// verb dials a server.
func TestUpgrade(t *testing.T) {
	dev := buildOnce(t)
	old := buildStamped(t, "0.1.0")
	fresh := buildStamped(t, "9.9.9")
	tarball := tarballOf(t, fresh)
	rel := newFakeRelease(t, "v9.9.9", tarball, checksumsFor(assetName(), tarball))
	// The install is a release build, one version behind the fake release, so
	// every path reports a move rather than the dev fallback.
	installed := newInstall(t, old)

	t.Run("check writes nothing", func(t *testing.T) {
		before := digest(t, installed.real)
		out, err := runInstall(installed, rel, "upgrade", "--check")
		if err != nil {
			t.Fatalf("upgrade --check: %v\n%s", err, out)
		}
		if want := "dm 0.1.0 to 9.9.9 is available\n"; out != want {
			t.Fatalf("upgrade --check = %q, want %q", out, want)
		}
		if after := digest(t, installed.real); after != before {
			t.Fatalf("--check changed the installed binary: %s -> %s", before, after)
		}
		if names := entries(t, filepath.Dir(installed.real)); len(names) != 1 {
			t.Fatalf("--check left files beside the target: %v", names)
		}
		if names := entries(t, installed.tmp()); len(names) != 0 {
			t.Fatalf("--check left temp files behind: %v", names)
		}
	})

	t.Run("installs the asset and keeps the link", func(t *testing.T) {
		out, err := runInstall(installed, rel, "upgrade")
		if err != nil {
			t.Fatalf("upgrade: %v\n%s", err, out)
		}
		for _, want := range []string{
			"dm 0.1.0 to 9.9.9\n",
			"installed " + installed.real + "\n",
			"live sessions keep the old binary until they restart\n",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("upgrade output %q misses %q", out, want)
			}
		}
		if got, want := digest(t, installed.real), digest(t, fresh); got != want {
			t.Fatalf("installed binary is not the tarball's dm: %s, want %s", got, want)
		}
		info, err := os.Stat(installed.real)
		if err != nil {
			t.Fatalf("stat %s: %v", installed.real, err)
		}
		// The install went in 0700, so 0755 here is the replace's chmod, not
		// the mode go build happened to give the file.
		if got := info.Mode().Perm(); got != 0o755 {
			t.Fatalf("installed mode = %o, want 0755", got)
		}
		target, err := os.Readlink(installed.link)
		if err != nil || target != "../real/dm" {
			t.Fatalf("symlink = %q, %v; want ../real/dm", target, err)
		}
		if names := entries(t, filepath.Dir(installed.real)); len(names) != 1 {
			t.Fatalf("upgrade left files beside the target: %v", names)
		}
		if names := entries(t, installed.tmp()); len(names) != 0 {
			t.Fatalf("upgrade left temp files behind: %v", names)
		}
		out, err = runInstall(installed, rel, "version")
		if err != nil {
			t.Fatalf("dm version through the link: %v\n%s", err, out)
		}
		if want := "dm 9.9.9 (" + runtime.GOOS + "/" + runtime.GOARCH + ")\n"; out != want {
			t.Fatalf("the link reports %q, want %q", out, want)
		}
	})

	t.Run("already current", func(t *testing.T) {
		before := digest(t, installed.real)
		out, err := runInstall(installed, rel, "upgrade")
		if err != nil {
			t.Fatalf("upgrade: %v\n%s", err, out)
		}
		if want := "dm 9.9.9 is current\n"; out != want {
			t.Fatalf("upgrade = %q, want %q", out, want)
		}
		out, err = runInstall(installed, rel, "upgrade", "--check")
		if err != nil {
			t.Fatalf("upgrade --check: %v\n%s", err, out)
		}
		if want := "dm 9.9.9 is current\n"; out != want {
			t.Fatalf("upgrade --check = %q, want %q", out, want)
		}
		if after := digest(t, installed.real); after != before {
			t.Fatalf("an already-current upgrade changed the binary: %s -> %s", before, after)
		}
	})

	// A dev build carries no version to compare, so it is older than every
	// release by definition. `go install` in a checkout must not strand the
	// operator on a binary that refuses to move.
	t.Run("dev build is upgraded", func(t *testing.T) {
		own := newInstall(t, dev)
		before := digest(t, own.real)
		out, err := runInstall(own, rel, "upgrade", "--check")
		if err != nil {
			t.Fatalf("upgrade --check on a dev build: %v\n%s", err, out)
		}
		if want := "dm dev to 9.9.9 is available\n"; out != want {
			t.Fatalf("upgrade --check = %q, want %q", out, want)
		}
		if after := digest(t, own.real); after != before {
			t.Fatalf("--check of a dev build changed the binary: %s -> %s", before, after)
		}
		out, err = runInstall(own, rel, "upgrade")
		if err != nil {
			t.Fatalf("upgrade of a dev build: %v\n%s", err, out)
		}
		if !strings.Contains(out, "dm dev to 9.9.9\n") || !strings.Contains(out, "installed "+own.real) {
			t.Fatalf("upgrade of a dev build printed %q, want the move and the path", out)
		}
		if got, want := digest(t, own.real), digest(t, fresh); got != want {
			t.Fatalf("a dev build was not replaced by the release: %s, want %s", got, want)
		}
		if target, err := os.Readlink(own.link); err != nil || target != "../real/dm" {
			t.Fatalf("symlink after a dev upgrade = %q, %v; want ../real/dm", target, err)
		}
	})

	t.Run("digest mismatch leaves the binary alone", func(t *testing.T) {
		own := newInstall(t, old)
		bad := newFakeRelease(t, "v9.9.9", tarball, checksumsFor(assetName(), []byte("not the tarball")))
		before := digest(t, own.real)
		info, err := os.Stat(own.real)
		if err != nil {
			t.Fatalf("stat %s: %v", own.real, err)
		}
		out, err := runInstall(own, bad, "upgrade")
		if err == nil {
			t.Fatalf("upgrade with a bad checksum exited 0:\n%s", out)
		}
		if !strings.Contains(out, "digest mismatch") {
			t.Fatalf("upgrade with a bad checksum printed %q, want a digest mismatch", out)
		}
		if after := digest(t, own.real); after != before {
			t.Fatalf("a refused upgrade changed the binary: %s -> %s", before, after)
		}
		after, err := os.Stat(own.real)
		if err != nil {
			t.Fatalf("stat %s: %v", own.real, err)
		}
		if after.Mode() != info.Mode() {
			t.Fatalf("a refused upgrade changed the mode: %v -> %v", info.Mode(), after.Mode())
		}
		if names := entries(t, filepath.Dir(own.real)); len(names) != 1 {
			t.Fatalf("a refused upgrade left files beside the target: %v", names)
		}
		if names := entries(t, own.tmp()); len(names) != 0 {
			t.Fatalf("a refused upgrade left temp files behind: %v", names)
		}
	})

	// The CLI names the asset from runtime.GOOS and runtime.GOARCH, which a
	// blackbox test cannot move: the binary under test is built for this host.
	// internal/release's TestAssetName owns the refusal of a pair outside the
	// matrix. The release that names no asset for this platform is the refusal
	// a blackbox test can reach, and it takes the same lookup path.
	t.Run("unsupported platform", func(t *testing.T) {
		t.Skip("internal/cli picks the asset from runtime.GOOS/GOARCH with no seam; TestAssetName in internal/release covers the unsupported pair")
	})

	t.Run("release with no asset for this platform", func(t *testing.T) {
		own := newInstall(t, old)
		none := newFakeRelease(t, "v9.9.9", tarball, checksumsFor(assetName(), tarball))
		none.asset = ""
		before := digest(t, own.real)
		out, err := runInstall(own, none, "upgrade")
		if err == nil {
			t.Fatalf("upgrade with no matching asset exited 0:\n%s", out)
		}
		if want := "has no " + assetName(); !strings.Contains(out, want) {
			t.Fatalf("upgrade with no matching asset printed %q, want %q", out, want)
		}
		if after := digest(t, own.real); after != before {
			t.Fatalf("a refused upgrade changed the binary: %s -> %s", before, after)
		}
	})
}

// capture runs one dm command against an environment, and returns what it
// printed with the error a non-zero exit makes.
func capture(bin string, env []string, args ...string) (string, error) {
	cmd := exec.Command(bin, args...)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// buildStamped builds the binary a release carries: buildOnce's inputs plus the
// linker stamp of a release, which is what the fake release advertises.
func buildStamped(t *testing.T, v string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "dm")
	cmd := exec.Command("go", "build", "-o", bin, "-ldflags", "-X dm/internal/version.Version="+v, ".")
	cmd.Dir = ".."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build -ldflags: %v\n%s", err, out)
	}
	return bin
}

// install is one dm install the way the operator has it: the real file in
// real/, a relative symlink to it in bin/, and bin/ first on PATH. The resolved
// file is the upgrade target, so the link has to survive the replace.
type install struct {
	root string // the temp tree holding real/, bin/ and tmp/
	real string // the resolved binary, the upgrade target
	link string // the relative symlink into bin/
}

func newInstall(t *testing.T, bin string) install {
	t.Helper()
	in := install{root: t.TempDir()}
	in.real = filepath.Join(in.root, "real", "dm")
	in.link = filepath.Join(in.root, "bin", "dm")
	for _, dir := range []string{filepath.Dir(in.real), filepath.Dir(in.link)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("install: %v", err)
		}
	}
	data, err := os.ReadFile(bin)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := os.WriteFile(in.real, data, 0o700); err != nil {
		t.Fatalf("install: %v", err)
	}
	// 0700 on purpose: only the replace can make it 0755, so the mode
	// assertion proves the write rather than the mode the file was copied in.
	if err := os.Chmod(in.real, 0o700); err != nil {
		t.Fatalf("install: %v", err)
	}
	if err := os.Symlink("../real/dm", in.link); err != nil {
		t.Fatalf("install: %v", err)
	}
	return in
}

// tmp is the install's own TMPDIR, so a leaked temp directory is visible to the
// test instead of landing in the host's /tmp.
func (in install) tmp() string { return filepath.Join(in.root, "tmp") }

// runInstall runs the installed dm with roots of its own: its own state and
// HOME, DM_RELEASE_API at the fake release, its bin/ first on PATH, and TMPDIR
// inside the install. exec keeps the last value of a repeated key, so the
// appended overrides win over the ones testEnv keeps in the base environment.
func runInstall(in install, rel *fakeRelease, args ...string) (string, error) {
	if err := os.MkdirAll(in.tmp(), 0o755); err != nil {
		return "", err
	}
	cmd := exec.Command(in.link, args...)
	cmd.Env = testEnv(os.Environ(),
		"DM_STATE="+filepath.Join(in.root, "state"),
		"HOME="+filepath.Join(in.root, "home"),
		"DM_RELEASE_API="+rel.url,
		"PATH="+filepath.Dir(in.link)+":"+os.Getenv("PATH"),
		"TMPDIR="+in.tmp(),
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// assetName is the release asset for the platform the test binary runs on. The
// verbs pick it from runtime.GOOS and runtime.GOARCH, which a blackbox test
// cannot move.
func assetName() string {
	return "dm_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz"
}

// fakeRelease serves one `latest` object, the tarball it names and its
// checksums.txt, all from one httptest server. DM_RELEASE_API points the verbs
// at it. An empty asset is a release that names no file for this platform.
type fakeRelease struct {
	url       string
	asset     string
	tarball   []byte
	checksums []byte
}

func newFakeRelease(t *testing.T, tag string, tarball, checksums []byte) *fakeRelease {
	t.Helper()
	rel := &fakeRelease{asset: assetName(), tarball: tarball, checksums: checksums}
	mux := http.NewServeMux()
	mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) {
		type asset struct {
			Name string `json:"name"`
			Size int64  `json:"size"`
			URL  string `json:"browser_download_url"`
		}
		payload := struct {
			TagName string  `json:"tag_name"`
			Assets  []asset `json:"assets"`
		}{TagName: tag}
		files := []struct {
			name string
			data []byte
		}{{rel.asset, rel.tarball}, {"checksums.txt", rel.checksums}}
		for _, f := range files {
			if f.name == "" {
				continue
			}
			payload.Assets = append(payload.Assets, asset{
				Name: f.name,
				Size: int64(len(f.data)),
				URL:  rel.url + "/" + f.name,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			t.Errorf("latest: %v", err)
		}
	})
	mux.HandleFunc("/"+assetName(), func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(rel.tarball)
	})
	mux.HandleFunc("/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(rel.checksums)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	rel.url = srv.URL
	return rel
}

// tarballOf packs one regular file named dm at the archive root, the shape the
// release workflow makes: tar -czf dm_<goos>_<goarch>.tar.gz -C out dm.
func tarballOf(t *testing.T, bin string) []byte {
	t.Helper()
	data, err := os.ReadFile(bin)
	if err != nil {
		t.Fatalf("tarball: %v", err)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	header := &tar.Header{Name: "dm", Mode: 0o755, Size: int64(len(data)), Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(header); err != nil {
		t.Fatalf("tarball: %v", err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatalf("tarball: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tarball: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("tarball: %v", err)
	}
	return buf.Bytes()
}

// checksumsFor renders a checksums.txt in sha256sum format for one asset.
func checksumsFor(name string, data []byte) []byte {
	sum := sha256.Sum256(data)
	return []byte(hex.EncodeToString(sum[:]) + "  " + name + "\n")
}

// digest is the sha256 of a file, so a test can say a binary did not move.
func digest(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("digest %s: %v", path, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// entries lists a directory's names, and fails when it cannot read it.
func entries(t *testing.T, dir string) []string {
	t.Helper()
	list, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	names := make([]string, 0, len(list))
	for _, e := range list {
		names = append(names, e.Name())
	}
	return names
}
