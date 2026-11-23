// Package webui serves a session to a phone: the fleet with its agent status,
// the box, and one pane's live terminal. It is a view of the socket contract,
// so it owns no state and adds no verb to the daemon.
//
// The surface is deliberately small. A phone is on the tailnet, not on the
// machine, so the token is the whole access model: a tailnet is shared with
// other people, and a port is reachable by everything on it.
package webui

import (
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

//go:embed static
var staticFS embed.FS

// tokenCookie keeps the token across the requests the page makes for itself.
// The first visit carries it in the query; every later fetch and the event
// stream ride the cookie, so the token appears once in the address bar.
const tokenCookie = "dm_web_token"

// Config is what dm web hands to Run.
type Config struct {
	// Addr is the listen address, host:port. Loopback unless the operator asks
	// for more.
	Addr string
	// Session and Root name the session to serve and the state tree it lives
	// in, so a test can serve a temp tree.
	Session string
	Root    string
	// Token is the access token. Run generates one when it is empty, so a
	// forgotten token is never an open port.
	Token string
	// ReadOnly refuses the write endpoints: keys and messages.
	ReadOnly bool
}

// Run serves cfg until the listener fails. It prints the URL that carries the
// token, because a token nobody can see is a token nobody uses.
func Run(cfg Config) error {
	if cfg.Token == "" {
		tok, err := newToken()
		if err != nil {
			return err
		}
		cfg.Token = tok
	}
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return err
	}
	url := urlFor(ln.Addr().String(), cfg.Token)
	fmt.Printf("dm web: session %s\n  %s\n", cfg.Session, url)
	if loopback(ln.Addr()) {
		fmt.Printf("  loopback only: pass --addr with the address a phone can reach\n")
	} else {
		fmt.Printf("  reachable from your tailnet; the token is the only guard\n")
		printQR(url)
	}
	srv := &http.Server{
		Handler:           NewHandler(cfg),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return srv.Serve(ln)
}

// newToken returns 16 random bytes as hex.
func newToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("dm web: token: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// urlFor renders the address a phone should open. A wildcard listener has no
// address of its own, so it is shown as the loopback URL every host answers.
func urlFor(addr, token string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://" + addr + "/?t=" + token
	}
	if host == "" || host == "::" || host == "0.0.0.0" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/?t=" + token
}

// loopback reports whether addr is a loopback address, so Run only warns about
// exposure when there is exposure.
func loopback(addr net.Addr) bool {
	a, ok := addr.(*net.TCPAddr)
	return ok && a.IP.IsLoopback()
}

// Handler is the whole HTTP surface. Handlers reach the session through the
// socket, so the web server holds no session state of its own.
type Handler struct {
	cfg Config
}

// NewHandler returns the authenticated surface for cfg.
func NewHandler(cfg Config) http.Handler {
	h := &Handler{cfg: cfg}
	files, err := fs.Sub(staticFS, "static")
	if err != nil {
		// The embed is part of the build, so a broken subtree is a programming
		// error, not a runtime condition.
		panic("webui: " + err.Error())
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", h.index)
	mux.Handle("GET /static/", noStore(http.StripPrefix("/static/", http.FileServerFS(files))))
	mux.HandleFunc("GET /api/fleet", h.fleet)
	mux.HandleFunc("GET /api/text", h.text)
	mux.HandleFunc("GET /api/commands", h.commands)
	mux.HandleFunc("POST /api/action", h.action)
	mux.HandleFunc("POST /api/msg", h.msg)
	return h.auth(mux)
}

// index serves the page and remembers a token that arrived in the query.
func (h *Handler) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("t") != "" {
		http.SetCookie(w, &http.Cookie{
			Name:     tokenCookie,
			Value:    h.cfg.Token,
			Path:     "/",
			MaxAge:   30 * 24 * 60 * 60,
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
		})
	}
	b, err := fs.ReadFile(staticFS, "static/index.html")
	if err != nil {
		http.Error(w, "dm web: no page", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(b)
}

// noStore keeps the browser off its cache for the page's own assets. They are
// embedded in the binary, so a new build must reach the phone on the next load;
// a phone holding yesterday's app.js is a bug report with no evidence in it.
func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// auth is the token check every route passes through. The query carries the
// token on the first visit, the cookie on every one after it, and a bearer
// header serves a script.
func (h *Handler) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !h.allowed(r) {
			log.Printf("dm web: refused %s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)
			w.Header().Set("WWW-Authenticate", `Bearer realm="dm"`)
			http.Error(w, "dm web: bad or missing token", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// allowed reports whether the request carries the token in any of its three
// forms. The comparison is constant time, so a token cannot be guessed a byte
// at a time.
func (h *Handler) allowed(r *http.Request) bool {
	if tok := r.URL.Query().Get("t"); tok != "" {
		return equal(tok, h.cfg.Token)
	}
	if c, err := r.Cookie(tokenCookie); err == nil && equal(c.Value, h.cfg.Token) {
		return true
	}
	if v, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		return equal(v, h.cfg.Token)
	}
	return false
}

// equal compares two tokens without leaking where they differ.
func equal(got, want string) bool {
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}
