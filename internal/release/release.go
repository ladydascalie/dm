// Package release is the GitHub side of a dm release, and the file work an
// upgrade needs: read the latest release, name the asset for a platform, check
// a digest, download, extract the binary, and replace the installed one.
//
// Nothing here prints or touches the terminal. Every function returns a value
// or an error, so the verbs own the text.
package release

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	// DefaultAPI is the base of the public release endpoint of dm. Latest
	// appends "/latest" to it.
	DefaultAPI = "https://api.github.com/repos/ladydascalie/dm/releases"

	// APIEnv overrides the base. The reader reads it once, in NewReader.
	// It is the one seam a test or a mirror needs.
	APIEnv = "DM_RELEASE_API"

	// maxBody is the largest release response this package will read.
	maxBody = 1 << 20

	// Timeout bounds one request, body included.
	Timeout = 30 * time.Second
)

// Asset is one downloadable file of a release.
type Asset struct {
	// Name is the file name, for example dm_linux_amd64.tar.gz.
	Name string
	// URL is the download URL GitHub serves the file at.
	URL string
	// Size is the length in bytes, as the release reports it.
	Size int64
}

// Release is one release: its tag and its files.
type Release struct {
	// Tag is the tag as GitHub reports it, leading "v" included.
	Tag string
	// Assets are the files of the release, in the order GitHub lists them.
	Assets []Asset
}

// Reader reads the release endpoint.
type Reader struct {
	// API is the base URL Latest appends "/latest" to.
	API string
	// Client makes the requests. A nil Client gets one with Timeout.
	Client *http.Client
}

// NewReader returns a Reader for the public dm release endpoint, or for the
// endpoint DM_RELEASE_API names when it is set.
//
// The variable names the releases base; a value that already ends in "/latest"
// is taken as the same base, because Latest is what appends the segment.
func NewReader() Reader {
	api := DefaultAPI
	if override := os.Getenv(APIEnv); override != "" {
		api = override
	}
	api = strings.TrimRight(api, "/")
	api = strings.TrimSuffix(api, "/latest")
	return Reader{API: api, Client: &http.Client{Timeout: Timeout}}
}

// Latest reads the newest release.
//
// It refuses a response over 1 MiB and any status other than 200.
func (r Reader) Latest(ctx context.Context) (Release, error) {
	api := strings.TrimRight(r.API, "/")
	if api == "" {
		return Release{}, errors.New("release: no API URL: set " + APIEnv)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, api+"/latest", nil)
	if err != nil {
		return Release{}, fmt.Errorf("release: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := r.client().Do(req)
	if err != nil {
		return Release{}, fmt.Errorf("release: latest: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return Release{}, fmt.Errorf("release: latest: %w", err)
	}
	if len(body) > maxBody {
		return Release{}, fmt.Errorf("release: latest: body over %d bytes", maxBody)
	}
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("release: latest: %s: %s", resp.Status, snippet(body))
	}
	var payload struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			Size int64  `json:"size"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return Release{}, fmt.Errorf("release: latest: %w", err)
	}
	out := Release{Tag: payload.TagName, Assets: make([]Asset, 0, len(payload.Assets))}
	for _, a := range payload.Assets {
		out.Assets = append(out.Assets, Asset{Name: a.Name, URL: a.URL, Size: a.Size})
	}
	return out, nil
}

// client returns the HTTP client to use, with Timeout when the Reader names
// none.
func (r Reader) client() *http.Client {
	if r.Client != nil {
		return r.Client
	}
	return &http.Client{Timeout: Timeout}
}

// snippet shortens an error body for a message.
func snippet(body []byte) string {
	s := strings.TrimSpace(string(body))
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}

// matrix is the platform list dm ships for. The workflow and the Makefile
// build the same four rows.
var matrix = []struct{ goos, goarch string }{
	{"linux", "amd64"},
	{"linux", "arm64"},
	{"darwin", "amd64"},
	{"darwin", "arm64"},
}

// AssetName returns the name of the release asset for one platform, for
// example dm_darwin_arm64.tar.gz. A pair outside the matrix is an error that
// names the pair it refused.
func AssetName(goos, goarch string) (string, error) {
	for _, p := range matrix {
		if p.goos == goos && p.goarch == goarch {
			return "dm_" + goos + "_" + goarch + ".tar.gz", nil
		}
	}
	return "", fmt.Errorf("release: no asset for %s/%s: dm ships %s", goos, goarch, matrixNames())
}

// matrixNames lists the matrix for an error message.
func matrixNames() string {
	names := make([]string, 0, len(matrix))
	for _, p := range matrix {
		names = append(names, p.goos+"/"+p.goarch)
	}
	return strings.Join(names, ", ")
}
