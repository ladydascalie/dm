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
func runWeb(c *cmdFlags) error {
	addr, fromConfig, err := webAddr(c)
	if err != nil {
		return err
	}
	announceWebAddr(os.Stdout, addr, fromConfig)
	return webui.Run(webui.Config{
		Addr:     addr,
		Session:  c.session,
		Root:     c.root,
		Token:    c.token,
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
