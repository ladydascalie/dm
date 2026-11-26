package webui

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"dm/internal/paths"
	"dm/internal/push"
)

// The push surface hands the phone's subscription to dm. The page subscribes
// through /api/push/subscribe, and the daemon reads the same file later, so the
// two processes agree on one path and one shape.

// maxPushBody bounds a subscription body. A real one is a few hundred bytes;
// anything larger is not a phone.
const maxPushBody = 4096

// pushConfigReply is the body of /api/push/config. It carries no path and no
// error text: a page that cannot push says so and nothing else.
type pushConfigReply struct {
	Enabled   bool   `json:"enabled"`
	PublicKey string `json:"publicKey"`
}

// pushState is the process-wide half of the notification surface: the VAPID
// key pair and the one Store.
//
// Both are created on first use and kept, because two Stores over the same file
// in one process would still race each other's reload, and the key pair is a
// file read per config fetch nobody needs.
type pushState struct {
	mu    sync.Mutex
	keys  *push.Keys
	store *push.Store
}

// sessionDir is the directory the session keeps its state in.
func (h *Handler) sessionDir() string {
	return paths.SessionDirAt(h.cfg.Root, h.cfg.Session)
}

// vapidKeys returns the session's key pair, creating it on first use. A nil
// pair means the pair could not be read or written: the caller reports push as
// unavailable, without naming the path or the error.
func (h *Handler) vapidKeys() *push.Keys {
	h.state.mu.Lock()
	defer h.state.mu.Unlock()
	if h.state.keys != nil {
		return h.state.keys
	}
	// A second call retries a pair that failed, because another process may
	// have written it in between.
	keys, err := push.LoadOrCreateKeys(push.KeysPath(h.sessionDir()))
	if err != nil {
		return nil
	}
	h.state.keys = keys
	return keys
}

// subscriptions returns the one Store this process reads and writes. A nil
// Store means the file could not be loaded, and the caller answers with an
// error instead of an empty list: an unreadable store must not look like a
// session with no phones, or the next write would drop them.
func (h *Handler) subscriptions() *push.Store {
	h.state.mu.Lock()
	defer h.state.mu.Unlock()
	if h.state.store != nil {
		return h.state.store
	}
	store, err := push.Load(push.StorePath(h.sessionDir()))
	if err != nil {
		return nil
	}
	h.state.store = store
	return store
}

// pushConfig reports whether this session can push, and the key the phone
// subscribes with. The key is the same on every call, so a page that reloads
// keeps the subscription it already holds.
func (h *Handler) pushConfig(w http.ResponseWriter, r *http.Request) {
	out := pushConfigReply{}
	if keys := h.vapidKeys(); keys != nil {
		out.Enabled = true
		out.PublicKey = keys.PublicKey()
	}
	writeJSON(w, http.StatusOK, out)
}

// pushSubscribe stores one phone's subscription. The endpoint is the identity,
// so a phone that subscribes again replaces its old entry instead of sounding
// twice.
func (h *Handler) pushSubscribe(w http.ResponseWriter, r *http.Request) {
	sub, reason := readSubscription(w, r)
	if reason != "" {
		http.Error(w, "dm web: "+reason, http.StatusBadRequest)
		return
	}
	store := h.subscriptions()
	if store == nil {
		http.Error(w, "dm web: push store unavailable", http.StatusInternalServerError)
		return
	}
	if err := store.Add(sub); err != nil {
		// The error names the file, never the endpoint or a secret.
		log.Printf("dm web: push subscribe: %v", err)
		http.Error(w, "dm web: push store unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// pushUnsubscribe forgets one endpoint. It is idempotent: a phone that
// unsubscribes twice, or one dm already pruned, still gets 204.
func (h *Handler) pushUnsubscribe(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Endpoint string `json:"endpoint"`
	}
	if err := readBody(w, r, &req); err != nil {
		http.Error(w, "dm web: malformed body", http.StatusBadRequest)
		return
	}
	if req.Endpoint == "" {
		http.Error(w, "dm web: endpoint required", http.StatusBadRequest)
		return
	}
	store := h.subscriptions()
	if store == nil {
		http.Error(w, "dm web: push store unavailable", http.StatusInternalServerError)
		return
	}
	if _, err := store.Remove(req.Endpoint); err != nil {
		log.Printf("dm web: push unsubscribe: %v", err)
		http.Error(w, "dm web: push store unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// readSubscription decodes and validates a subscription body. It returns a
// short reason instead of an error, because the reason is the whole response: a
// rejected body gets one line and never echoes what it sent.
func readSubscription(w http.ResponseWriter, r *http.Request) (push.Subscription, string) {
	var sub push.Subscription
	if err := readBody(w, r, &sub); err != nil {
		return push.Subscription{}, "malformed body"
	}
	if !pushEndpoint(sub.Endpoint) {
		return push.Subscription{}, "endpoint must be https, or http on loopback"
	}
	if !pushP256DH(sub.P256DH) {
		return push.Subscription{}, "p256dh must be a 65-byte uncompressed point"
	}
	if !pushAuth(sub.Auth) {
		return push.Subscription{}, "auth must be 16 bytes"
	}
	sub.At = time.Now().UTC()
	return sub, ""
}

// readBody decodes one small JSON body into v. The limit is what keeps a huge
// body from being decoded before it is rejected.
func readBody(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxPushBody)
	return json.NewDecoder(r.Body).Decode(v)
}

// pushEndpoint reports whether raw is an absolute push endpoint: https
// anywhere, or http on a loopback host, which is what a test server answers.
func pushEndpoint(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false
	}
	switch u.Scheme {
	case "https":
		return true
	case "http":
		return loopbackHost(u.Hostname())
	}
	return false
}

// loopbackHost reports whether host names this machine.
func loopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// pushP256DH reports whether raw is the phone's public key: base64url of a
// 65-byte uncompressed P-256 point.
func pushP256DH(raw string) bool {
	b, err := decodePushBase64(raw)
	return err == nil && len(b) == 65 && b[0] == 0x04
}

// pushAuth reports whether raw is the phone's auth secret: base64url of 16
// bytes.
func pushAuth(raw string) bool {
	b, err := decodePushBase64(raw)
	return err == nil && len(b) == 16
}

// decodePushBase64 decodes the raw base64url a browser reports, and tolerates
// padding a client may have added.
func decodePushBase64(raw string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(raw, "="))
}

// The icon sizes the page and the manifest ask for: the two the manifest
// declares, and the one iOS reads for a home-screen icon.
const (
	iconApple = 180
	iconSmall = 192
	iconLarge = 512
)

// iconLen is the cache key set: one drawing per size, kept for the life of the
// process. The drawing is deterministic, so a cached icon and a fresh one are
// the same bytes.
var iconSizes = [...]int{iconApple, iconSmall, iconLarge}

// iconONCE keeps the drawn icons. The first request for a size draws it, every
// later one serves the bytes.
type iconCache struct {
	mu    sync.Mutex
	bytes map[int][]byte
}

// icon serves the size's PNG.
func (h *Handler) icon(size int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := h.icons.at(size)
		if err != nil {
			http.Error(w, "dm web: no icon", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(b)
	}
}

// at returns the PNG for size, drawing it once.
func (c *iconCache) at(size int) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if b, ok := c.bytes[size]; ok {
		return b, nil
	}
	b, err := drawIcon(size)
	if err != nil {
		return nil, err
	}
	if c.bytes == nil {
		c.bytes = make(map[int][]byte, len(iconSizes))
	}
	c.bytes[size] = b
	return b, nil
}

// The icon's two colours: the page's background and its cyan accent. The mark
// is a ring, drawn inside the middle 68% so the maskable icon keeps its mark
// whatever shape the phone masks it to.
var (
	iconBackground = color.RGBA{R: 0x10, G: 0x14, B: 0x18, A: 0xff}
	iconAccent     = color.RGBA{R: 0x6e, G: 0xdc, B: 0xeb, A: 0xff}
)

// iconSamples is how many points per axis one pixel is measured at. It is the
// only smoothing the mark gets, and it is fixed, so the bytes do not change
// between builds.
const iconSamples = 4

// drawIcon renders one icon: the dark background with the accent ring, drawn in
// integer arithmetic so the same size always encodes to the same bytes. No
// binary blob lives in the repository because of it.
func drawIcon(size int) ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	// The radii are in whole pixels, so the ring is the same shape at every
	// size up to the pixel grid: 34% out, 20% in.
	outer := size * 34 / 100
	inner := size * 20 / 100
	center := size / 2
	area := iconSamples * iconSamples
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			hits := 0
			for sy := 0; sy < iconSamples; sy++ {
				for sx := 0; sx < iconSamples; sx++ {
					dx := iconSamples*(x-center) + sx
					dy := iconSamples*(y-center) + sy
					d := dx*dx + dy*dy
					if d > (outer*iconSamples)*(outer*iconSamples) {
						continue
					}
					if d <= (inner*iconSamples)*(inner*iconSamples) {
						continue
					}
					hits++
				}
			}
			img.SetRGBA(x, y, blend(iconBackground, iconAccent, hits, area))
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// blend mixes both colours at hits/area, which is the pixel's coverage.
func blend(from, to color.RGBA, hits, area int) color.RGBA {
	mix := func(a, b uint8) uint8 {
		return uint8(int(a) + (int(b)-int(a))*hits/area)
	}
	return color.RGBA{R: mix(from.R, to.R), G: mix(from.G, to.G), B: mix(from.B, to.B), A: 0xff}
}
