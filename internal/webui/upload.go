package webui

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"dm/internal/paths"
)

// maxUpload is the largest picture the page may send. A phone photo is a few
// megabytes, so anything far past that is a mistake or a probe.
const maxUpload = 10 << 20

// imageExt names the extension for each type the page accepts, keyed by the
// type the bytes themselves carry. The browser's header is never trusted: a
// text file called .png is refused and a real picture with no name is kept.
var imageExt = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

// upload stores one picture the page sends and answers with its absolute path.
// A pane cannot receive a picture, so the page types the path into the pane's
// own input and the agent reads it: pi's read tool sends those four types to
// the model as attachments.
func (h *Handler) upload(w http.ResponseWriter, r *http.Request) {
	if h.cfg.ReadOnly {
		http.Error(w, "dm web: read only", http.StatusForbidden)
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxUpload))
	if err != nil {
		http.Error(w, "dm web: picture is larger than 10 MB", http.StatusRequestEntityTooLarge)
		return
	}
	if len(data) == 0 {
		http.Error(w, "dm web: empty upload", http.StatusBadRequest)
		return
	}
	kind := http.DetectContentType(data)
	ext, ok := imageExt[kind]
	if !ok {
		http.Error(w, fmt.Sprintf("dm web: %s is not a picture: want png, jpeg, gif or webp", kind), http.StatusBadRequest)
		return
	}
	// The folder belongs to the session, so a photo of one session's work never
	// turns up in another's folder.
	dir := filepath.Join(paths.SessionDirAt(h.cfg.Root, h.cfg.Session), "uploads")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		http.Error(w, "dm web: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// The name is the picture's own hash, so the same photo twice costs one
	// file and a retry after a dropped reply finds what it already sent.
	sum := sha256.Sum256(data)
	path := filepath.Join(dir, hex.EncodeToString(sum[:16])+ext)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		http.Error(w, "dm web: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": path})
}
