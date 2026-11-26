package cli

// dm version and dm upgrade. version reports the build stamp the linker wrote.
// upgrade moves the running binary to the latest release, through the same
// path a symlinked install uses: the resolved file is the target, so the link
// the operator typed keeps pointing at it.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"dm/internal/release"
	"dm/internal/version"
)

// checksumAsset is the release asset carrying the sha256 digests, written by
// make dist and verified by release.Download.
const checksumAsset = "checksums.txt"

// maxChecksums bounds the checksums.txt read, the same order as the release
// payload.
const maxChecksums = 1 << 20

// versionReport is the --json object of dm version. One object, no wrapper.
type versionReport struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
}

// runVersion prints the build stamp and the platform it runs on. A stamped
// build appends "+<commit>"; a dev build prints just its version.
func runVersion(c *cmdFlags) error {
	if c.jsonOut {
		out, err := json.Marshal(versionReport{
			Version: version.Version,
			Commit:  version.Commit,
			OS:      runtime.GOOS,
			Arch:    runtime.GOARCH,
		})
		if err != nil {
			return fmt.Errorf("version: %w", err)
		}
		fmt.Println(string(out))
		return nil
	}
	fmt.Printf("dm %s (%s/%s)\n", version.String(), runtime.GOOS, runtime.GOARCH)
	return nil
}

// runUpgrade replaces the running binary with the latest release. --check
// reports and writes nothing. --force installs even when the build carries no
// release stamp or is already current.
func runUpgrade(c *cmdFlags) error {
	target, err := installedBinary()
	if err != nil {
		return err
	}
	if !version.IsRelease(version.Version) && !c.force {
		return fmt.Errorf("upgrade: %s: dm %s is a dev build, it has no release to move to; pass --force to install the latest release anyway", target, version.String())
	}
	ctx := context.Background()
	reader := release.NewReader()
	rel, err := reader.Latest(ctx)
	if err != nil {
		return fmt.Errorf("upgrade: %w", err)
	}
	to := strings.TrimPrefix(rel.Tag, "v")
	if to == "" {
		return fmt.Errorf("upgrade: the latest release carries no tag")
	}
	current := version.Compare(to, version.Version) <= 0
	if c.check {
		if current {
			fmt.Printf("dm %s is current\n", version.String())
		} else {
			fmt.Printf("dm %s to %s is available\n", version.String(), to)
		}
		return nil
	}
	if current && !c.force {
		fmt.Printf("dm %s is current\n", version.String())
		return nil
	}
	if err := installRelease(ctx, reader.Client, rel, target); err != nil {
		return err
	}
	fmt.Printf("dm %s to %s\n", version.String(), to)
	fmt.Printf("installed %s\n", target)
	fmt.Println("live sessions keep the old binary until they restart")
	return nil
}

// installedBinary resolves the running binary through its symlinks. The
// resolved path is the upgrade target: the operator's install is a link into a
// bin directory, and replacing the file it points at leaves the link in place.
func installedBinary() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("upgrade: cannot find the running binary: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err != nil {
		return "", fmt.Errorf("upgrade: cannot resolve %s: %w", exe, err)
	}
	return resolved, nil
}

// installRelease downloads the release asset for this platform, verifies its
// digest, extracts the binary beside the target and renames it into place.
//
// Nothing touches the target's directory until the digest matched, and both
// temp directories go in a deferred cleanup, so a failure leaves the old
// binary where it was.
func installRelease(ctx context.Context, client *http.Client, rel release.Release, target string) error {
	name, err := release.AssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return fmt.Errorf("upgrade: %w", err)
	}
	asset, ok := findAsset(rel, name)
	if !ok {
		return fmt.Errorf("upgrade: release %s has no %s", rel.Tag, name)
	}
	checksums, err := fetchChecksums(ctx, client, rel)
	if err != nil {
		return err
	}
	// The download lands outside the target's directory, and Download removes
	// it on a digest mismatch.
	dir, err := os.MkdirTemp("", "dm-upgrade-*")
	if err != nil {
		return fmt.Errorf("upgrade: %w", err)
	}
	defer os.RemoveAll(dir)
	tarball := filepath.Join(dir, name)
	if err := release.Download(ctx, client, asset, checksums, tarball); err != nil {
		return fmt.Errorf("upgrade: %w", err)
	}
	// The digest matched: the fresh binary is verified, so the target's
	// directory may take the extract and the rename.
	beside, err := os.MkdirTemp(filepath.Dir(target), ".dm-upgrade-*")
	if err != nil {
		return fmt.Errorf("upgrade: %s: %w", filepath.Dir(target), err)
	}
	defer os.RemoveAll(beside)
	fresh, err := release.Extract(tarball, beside)
	if err != nil {
		return fmt.Errorf("upgrade: %w", err)
	}
	if err := release.Replace(target, fresh); err != nil {
		return fmt.Errorf("upgrade: %w", err)
	}
	return nil
}

// findAsset returns the asset with this name from the release's own list.
func findAsset(rel release.Release, name string) (release.Asset, bool) {
	for _, a := range rel.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return release.Asset{}, false
}

// fetchChecksums reads checksums.txt from the release's own asset list, so the
// digests and the tarball come from the same release.
func fetchChecksums(ctx context.Context, client *http.Client, rel release.Release) ([]byte, error) {
	asset, ok := findAsset(rel, checksumAsset)
	if !ok {
		return nil, fmt.Errorf("upgrade: release %s has no %s", rel.Tag, checksumAsset)
	}
	if asset.URL == "" {
		return nil, fmt.Errorf("upgrade: release %s: %s has no URL", rel.Tag, checksumAsset)
	}
	if client == nil {
		client = &http.Client{Timeout: release.Timeout}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
	if err != nil {
		return nil, fmt.Errorf("upgrade: %s: %w", checksumAsset, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("upgrade: %s: %w", checksumAsset, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upgrade: %s: %s", checksumAsset, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxChecksums+1))
	if err != nil {
		return nil, fmt.Errorf("upgrade: %s: %w", checksumAsset, err)
	}
	if len(body) > maxChecksums {
		return nil, fmt.Errorf("upgrade: %s: over %d bytes", checksumAsset, maxChecksums)
	}
	return body, nil
}
