package release

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// digestOf returns the sha256 of s in the form checksums.txt carries.
func digestOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestChecksum(t *testing.T) {
	const body = "a dm tarball"
	digest := digestOf(body)
	file := "dm_linux_amd64.tar.gz"
	tests := []struct {
		name      string
		checksums []byte
		file      string
		want      string
		wantErr   string
	}{
		{
			name:      "one line",
			checksums: []byte(digest + "  " + file + "\n"),
			file:      file,
			want:      digest,
		},
		{
			name:      "picks the right line",
			checksums: []byte(digestOf("x") + "  other.tar.gz\n" + digest + "  " + file + "\n"),
			file:      file,
			want:      digest,
		},
		{
			name:      "crc line endings",
			checksums: []byte(digest + "  " + file + "\r\n"),
			file:      file,
			want:      digest,
		},
		{
			name:      "binary marker",
			checksums: []byte(digest + " *" + file + "\n"),
			file:      file,
			want:      digest,
		},
		{
			name:      "upper case digest",
			checksums: []byte(strings.ToUpper(digest) + "  " + file + "\n"),
			file:      file,
			want:      digest,
		},
		{
			name:      "no line for the asset",
			checksums: []byte(digest + "  other.tar.gz\n"),
			file:      file,
			wantErr:   "no line for " + file,
		},
		{
			name:      "empty file",
			checksums: nil,
			file:      file,
			wantErr:   "no line for " + file,
		},
		{
			name:      "listed twice",
			checksums: []byte(digest + "  " + file + "\n" + digest + "  " + file + "\n"),
			file:      file,
			wantErr:   "listed twice",
		},
		{
			name:      "not a sha256",
			checksums: []byte("deadbeef  " + file + "\n"),
			file:      file,
			wantErr:   "not a sha256 digest",
		},
		{
			name:      "not hex",
			checksums: []byte(strings.Repeat("z", 64) + "  " + file + "\n"),
			file:      file,
			wantErr:   "not a sha256 digest",
		},
		{
			name:      "empty name",
			checksums: []byte(digest + "  " + file + "\n"),
			file:      "",
			wantErr:   "empty asset name",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Checksum(tc.checksums, tc.file)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want one naming %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("Checksum = %q, want %q", got, tc.want)
			}
		})
	}
}

// assetServer serves body at the asset path and returns a release asset naming
// it.
func assetServer(t *testing.T, body string) (Asset, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "fail") {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	return Asset{Name: "dm_linux_amd64.tar.gz", URL: srv.URL + "/dm", Size: int64(len(body))}, srv
}

func TestDownload(t *testing.T) {
	const body = "a dm tarball"
	asset, srv := assetServer(t, body)
	dst := filepath.Join(t.TempDir(), "dm.tar.gz")
	checksums := []byte(digestOf(body) + "  " + asset.Name + "\n")
	if err := Download(context.Background(), srv.Client(), asset, checksums, dst); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != body {
		t.Fatalf("dst = %q, want %q", got, body)
	}
}

func TestDownloadRefusals(t *testing.T) {
	const body = "a dm tarball"
	tests := []struct {
		name      string
		checksums string
		asset     func(Asset) Asset
		wantIn    string
	}{
		{
			name:      "digest mismatch",
			checksums: digestOf("something else") + "  dm_linux_amd64.tar.gz\n",
			asset:     func(a Asset) Asset { return a },
			wantIn:    "digest mismatch",
		},
		{
			name:      "no line for the asset",
			checksums: digestOf(body) + "  other.tar.gz\n",
			asset:     func(a Asset) Asset { return a },
			wantIn:    "no line for",
		},
		{
			name:      "server error",
			checksums: digestOf(body) + "  dm_linux_amd64.tar.gz\n",
			asset:     func(a Asset) Asset { a.URL += "-fail"; return a },
			wantIn:    "500",
		},
		{
			name:      "short body",
			checksums: digestOf(body) + "  dm_linux_amd64.tar.gz\n",
			asset:     func(a Asset) Asset { a.Size = a.Size + 10; return a },
			wantIn:    "release says",
		},
		{
			name:      "no url",
			checksums: digestOf(body) + "  dm_linux_amd64.tar.gz\n",
			asset:     func(a Asset) Asset { a.URL = ""; return a },
			wantIn:    "no URL",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			asset, srv := assetServer(t, body)
			asset = tc.asset(asset)
			dir := t.TempDir()
			dst := filepath.Join(dir, "dm.tar.gz")
			err := Download(context.Background(), srv.Client(), asset, []byte(tc.checksums), dst)
			if err == nil {
				t.Fatal("Download returned no error")
			}
			if !strings.Contains(err.Error(), tc.wantIn) {
				t.Fatalf("error = %q, want it to name %q", err, tc.wantIn)
			}
			if _, statErr := os.Stat(dst); !os.IsNotExist(statErr) {
				t.Fatalf("dst survived a refused download: %v", statErr)
			}
		})
	}
}

func TestDownloadTruncatedBody(t *testing.T) {
	const body = "a dm tarball"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		fmt.Fprint(w, body)
	}))
	defer srv.Close()
	asset := Asset{Name: "dm_linux_amd64.tar.gz", URL: srv.URL, Size: 100}
	checksums := []byte(digestOf(body) + "  " + asset.Name + "\n")
	dst := filepath.Join(t.TempDir(), "dm.tar.gz")
	if err := Download(context.Background(), srv.Client(), asset, checksums, dst); err == nil {
		t.Fatal("Download returned no error for a truncated body")
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatalf("dst survived a truncated body: %v", err)
	}
}

func TestDownloadNilClient(t *testing.T) {
	const body = "a dm tarball"
	asset, _ := assetServer(t, body)
	dst := filepath.Join(t.TempDir(), "dm.tar.gz")
	checksums := []byte(digestOf(body) + "  " + asset.Name + "\n")
	if err := Download(context.Background(), nil, asset, checksums, dst); err != nil {
		t.Fatal(err)
	}
}
