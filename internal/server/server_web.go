package server

import (
	"errors"
	"log"
	"net/http"

	"dm/internal/webui"
)

// webInfoReply is the web-info answer: whether the daemon holds a listener
// right now, the link and address it bound when it does, and the bind failure
// when it tried and failed. The answer describes the listener, not the config
// file: a reload may flip web-auto after startWeb ran, and a client that read
// the flag would print a URL nothing serves, or bind a port the daemon holds.
type webInfoReply struct {
	Auto  bool   `json:"auto"`
	URL   string `json:"url"`
	Addr  string `json:"addr"`
	Error string `json:"error"`
}

// startWeb binds the always-on web listener when the config asks for it. The
// listener belongs to the daemon, so the URL outlives every detach and a
// `dm web` in a pane reads it from web-info instead of binding a second one on
// the same address. A failed bind is kept and logged, never fatal: the session
// runs without its page.
func (s *server) startWeb(root string) {
	s.mu.Lock()
	cfg, name := s.cfg, s.st.Name
	s.mu.Unlock()
	if !cfg.WebAuto() {
		return
	}
	// A pinned config token wins, the way `dm web --token` does. Without one
	// the session's web.token file decides, so the URL survives a restart.
	token := ""
	if cfg.WebTokenSet() {
		token = cfg.WebToken()
	}
	srv, err := webui.Listen(webui.Config{
		Addr:    cfg.WebAddr(),
		Session: name,
		Root:    root,
		Token:   token,
	})
	if err != nil {
		s.mu.Lock()
		s.webErr = err
		s.mu.Unlock()
		log.Printf("web: %v", err)
		return
	}
	s.mu.Lock()
	s.web = srv
	s.webURL = srv.URL()
	s.mu.Unlock()
	log.Printf("web: %s", srv.URL())
	go func() {
		if err := srv.Serve(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("web: %v", err)
		}
	}()
}

// closeWeb stops the always-on listener and forgets it, so a second call never
// touches a closed server. A server that never bound one costs nothing.
func (s *server) closeWeb() {
	s.mu.Lock()
	srv := s.web
	s.web, s.webURL = nil, ""
	s.mu.Unlock()
	if srv != nil {
		_ = srv.Close()
	}
}

// webInfo reports where the daemon's web listener stands. It is a read: the
// daemon owns the listener, so a client asks rather than guessing from its own
// config copy.
//
// auto is the listener, never the flag. A daemon started with web-auto off and
// reloaded on holds no listener and says so, and one whose listener still
// serves after a reload turned the flag off keeps saying true, because the port
// is taken and `dm web` must not try to bind it. A bind that failed leaves no
// listener either; the error names why.
func (s *server) webInfo() webInfoReply {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.web == nil {
		out := webInfoReply{}
		if s.webErr != nil {
			out.Error = s.webErr.Error()
		}
		return out
	}
	return webInfoReply{
		Auto: true,
		URL:  s.webURL,
		Addr: s.web.Addr().String(),
	}
}
