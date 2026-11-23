// Package webui serves a session to a phone: the fleet with its agent status,
// the box, and one pane's live terminal. It is a view of the socket contract,
// so it adds no verb to the daemon. Its only state is the token it keeps in
// the session directory, so a restart does not rotate the URL.
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
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dm/internal/paths"
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
	// SessionsDir is the pi sessions tree /api/session may read. Empty means
	// ~/.pi/agent/sessions. Nothing outside it is ever opened.
	SessionsDir string
	// Token is the access token. Run generates one when it is empty, so a
	// forgotten token is never an open port.
	Token string
	// ReadOnly refuses the write endpoints: keys and messages.
	ReadOnly bool
}

// Run serves cfg until the listener fails. It prints the URL that carries the
// token, because a token nobody can see is a token nobody uses.
func Run(cfg Config) error {
	tok, reused, err := webToken(cfg.Root, cfg.Session, cfg.Token)
	if err != nil {
		return err
	}
	cfg.Token = tok
	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return err
	}
	announce(cfg.Session, ln.Addr(), tok, reused)
	srv := &http.Server{
		Handler:           NewHandler(cfg),
		ReadHeaderTimeout: 10 * time.Second,
	}
	return srv.Serve(ln)
}

// announce prints what the operator needs to open the page: the URL that
// carries the token, whether the token survived a restart, and how far the
// listener reaches.
func announce(session string, addr net.Addr, token string, reused bool) {
	url := urlFor(addr.String(), token)
	fmt.Printf("dm web: session %s\n  %s\n", session, url)
	if reused {
		fmt.Printf("  token reused from the session file: this URL survives a restart\n")
	}
	if loopback(addr) {
		fmt.Printf("  loopback only: pass --addr with the address a phone can reach\n")
		return
	}
	fmt.Printf("  reachable from your tailnet; the token is the only guard\n")
	printQR(url)
}

// webToken returns the token this run serves and whether it came from the
// session file. An explicit token wins and is kept for the next start.
//
// The file is what lets a phone survive a restart: the URL it already has in
// its history keeps working. The file is a convenience, not the access rule,
// so a session directory that refuses it still serves. A page nobody can open
// is worse than a token that changes.
func webToken(root, session, explicit string) (tok string, reused bool, err error) {
	path := webTokenPath(root, session)
	if explicit != "" {
		if werr := writeTokenFile(path, explicit); werr != nil {
			warnToken(werr, "this URL changes on the next start of dm web")
		}
		return explicit, false, nil
	}
	if tok, rerr := readTokenFile(path); rerr == nil {
		return tok, true, nil
	} else if !errors.Is(rerr, fs.ErrNotExist) {
		warnToken(rerr, "generating a token for this run")
	}
	tok, err = newToken()
	if err != nil {
		return "", false, err
	}
	if werr := writeTokenFile(path, tok); werr != nil {
		warnToken(werr, "this URL changes on the next start of dm web")
	}
	return tok, false, nil
}

// webTokenPath is where a session keeps its token between starts.
func webTokenPath(root, session string) string {
	return filepath.Join(paths.SessionDirAt(root, session), "web.token")
}

// readTokenFile returns the token a previous run kept. A blank file is not a
// token, so it reads as unusable instead of as an open port.
func readTokenFile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	tok := strings.TrimSpace(string(b))
	if tok == "" {
		return "", fmt.Errorf("%s is empty", path)
	}
	return tok, nil
}

// writeTokenFile keeps tok for the next start. The mode is 0600 at creation
// and after, so the token is never readable by anyone but the operator.
func writeTokenFile(path, tok string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := fmt.Fprintln(f, tok); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// warnToken reports a token file dm web could not use. It is not fatal, so the
// message names what is lost rather than failing the run.
func warnToken(err error, consequence string) {
	fmt.Fprintf(os.Stderr, "dm web: token file: %v\n  %s\n", err, consequence)
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
	mux.HandleFunc("GET /api/session", h.session)
	mux.HandleFunc("GET /api/thumbs", h.thumbs)
	mux.HandleFunc("GET /api/commands", h.commands)
	mux.HandleFunc("POST /api/action", h.action)
	mux.HandleFunc("POST /api/msg", h.msg)
	mux.HandleFunc("POST /api/upload", h.upload)
	mux.HandleFunc("POST /api/keys", h.keys)
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
			// The page is where a phone lands after the token rotated. Give
			// that one GET the form that recovers. Every other request, and
			// every other method, keeps the plain refusal.
			if r.Method == http.MethodGet && r.URL.Path == "/" {
				recoveryPage(w)
				return
			}
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
