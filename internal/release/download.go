package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// Checksum returns the sha256 digest checksums records for name.
//
// checksums is a checksums.txt in sha256sum format, one "<digest>  <name>"
// line per file. The digest comes back lower case. A missing line, more than
// one line for the same name, or a digest that is not 64 hex characters is an
// error.
func Checksum(checksums []byte, name string) (string, error) {
	if name == "" {
		return "", errors.New("release: checksum: empty asset name")
	}
	var found string
	for _, line := range strings.Split(string(checksums), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// sha256sum marks a file read in binary mode with a leading "*".
		file := strings.TrimPrefix(strings.Join(fields[1:], " "), "*")
		if file != name {
			continue
		}
		digest := strings.ToLower(fields[0])
		if !digestShape(digest) {
			return "", fmt.Errorf("release: checksum: %s: %q is not a sha256 digest", name, fields[0])
		}
		if found != "" {
			return "", fmt.Errorf("release: checksum: %s is listed twice", name)
		}
		found = digest
	}
	if found == "" {
		return "", fmt.Errorf("release: checksum: no line for %s", name)
	}
	return found, nil
}

// digestShape reports whether s is 64 lower-case hex characters.
func digestShape(s string) bool {
	if len(s) != sha256.Size*2 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f':
		default:
			return false
		}
	}
	return true
}

// Download streams asset to dst, hashing as it writes, and compares the digest
// with the one checksums records for asset.Name.
//
// A failed request, a short body or a digest that does not match leaves no
// file behind: dst is removed. A mismatch names both digests.
func Download(ctx context.Context, client *http.Client, asset Asset, checksums []byte, dst string) error {
	want, err := Checksum(checksums, asset.Name)
	if err != nil {
		return err
	}
	if asset.URL == "" {
		return fmt.Errorf("release: download: %s has no URL", asset.Name)
	}
	if client == nil {
		client = &http.Client{Timeout: Timeout}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
	if err != nil {
		return fmt.Errorf("release: download %s: %w", asset.Name, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("release: download %s: %w", asset.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("release: download %s: %s", asset.Name, resp.Status)
	}
	file, err := os.Create(dst)
	if err != nil {
		return fmt.Errorf("release: download %s: %w", asset.Name, err)
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), resp.Body)
	closeErr := file.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return discard(dst, fmt.Errorf("release: download %s: %w", asset.Name, err))
	}
	got := hex.EncodeToString(hash.Sum(nil))
	if got != want {
		return discard(dst, fmt.Errorf("release: download %s: digest mismatch: want %s, got %s", asset.Name, want, got))
	}
	if asset.Size > 0 && written != asset.Size {
		return discard(dst, fmt.Errorf("release: download %s: %d bytes, release says %d", asset.Name, written, asset.Size))
	}
	return nil
}

// discard removes a half-written file and returns err, or returns err alone
// when the removal fails too.
func discard(path string, err error) error {
	if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) {
		return errors.Join(err, rmErr)
	}
	return err
}
