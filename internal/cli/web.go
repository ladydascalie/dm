package cli

import (
	"flag"
	"fmt"
	"io"
	"os"

	"dm/internal/keymap"
	"dm/internal/webui"
)

// runWeb serves the session to a browser, so a phone can watch the fleet and
// one pane without holding a terminal. It blocks until the listener fails.
//
// A bare `dm web` first asks the running daemon: when the config turned the
// listener on there, the daemon already owns the address, and a second one
// would only fail. An explicit flag keeps today's path, so an operator can
// always bind their own.
func runWeb(c *cmdFlags) error {
	explicit := c.flagGiven("addr") || c.flagGiven("token")
	info, answered := webInfo{}, false
	if !explicit {
		info, answered = daemonWeb(c.root, c.session)
	}
	if url, served := webHandledByDaemon(explicit, info, answered); served {
		announceWebServed(os.Stdout, c.session, url)
		return nil
	}
	addr, fromConfig, err := webAddr(c)
	if err != nil {
		return err
	}
	token, tokenFromConfig := webToken(c)
	announceWebAddr(os.Stdout, addr, fromConfig)
	announceWebToken(os.Stdout, tokenFromConfig)
	return webui.Run(webui.Config{
		Addr:     addr,
		Session:  c.session,
		Root:     c.root,
		Token:    token,
		ReadOnly: c.readOnly,
	})
}

// webAddr resolves the listen address. An explicit --addr wins, so the flag's
// Changed bit decides and not a comparison against the default:
// `dm web --addr 127.0.0.1:8099` is respected. Without the flag the config's
// web-addr decides, and its absence keeps the built-in default. It reports
// whether the config supplied the address, so the caller can say where the
// listener came from.
func webAddr(c *cmdFlags) (string, bool, error) {
	if c.flagGiven("addr") {
		return c.addr, false, nil
	}
	cfg, err := keymap.LoadDefaultPath(false)
	if err != nil {
		// A config that does not parse is refused rather than run: a listener
		// that silently fell back to loopback would hide the key the operator
		// set. The loader's error names the line and the value.
		return "", false, err
	}
	return cfg.WebAddr(), cfg.WebAddrSet(), nil
}

// webToken resolves the access token. An explicit --token wins, so a one-off
// run never rewrites the pinned one. Without the flag the config's web-token
// decides, and a config that never names it returns "", which lets the session
// file keep the token the last run used.
func webToken(c *cmdFlags) (string, bool) {
	if c.flagGiven("token") {
		return c.token, false
	}
	cfg, err := keymap.LoadDefaultPath(false)
	if err != nil {
		// webAddr already refuses an unparsable config, so this run has a
		// listener address and a broken config only in a test that calls the
		// resolver alone. A generated token is still better than none.
		return "", false
	}
	if !cfg.WebTokenSet() {
		return "", false
	}
	return cfg.WebToken(), true
}

// announceWebToken prints where the token came from, so an operator who cannot
// find the URL knows which node pins it.
func announceWebToken(w io.Writer, fromConfig bool) {
	if !fromConfig {
		return
	}
	fmt.Fprintln(w, "dm web: token from config.kdl web-token: this URL never changes")
}

// flagGiven reports whether argv carried the named flag. It reads the flag
// set's Changed bit through Visit, which visits only the flags that were set,
// so an explicit value equal to the default still counts.
func (c *cmdFlags) flagGiven(name string) bool {
	given := false
	c.fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			given = true
		}
	})
	return given
}

// announceWebAddr prints where the listen address came from, once, when the
// config supplied it. A tailnet address is not obvious from the printed URL
// alone, so the line names the key an operator would edit to change it.
func announceWebAddr(w io.Writer, addr string, fromConfig bool) {
	if !fromConfig {
		return
	}
	fmt.Fprintf(w, "dm web: addr %s from config.kdl web-addr\n", addr)
}
