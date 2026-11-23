package cli

import "dm/internal/webui"

// runWeb serves the session to a browser, so a phone can watch the fleet and
// one pane without holding a terminal. It blocks until the listener fails.
func runWeb(c *cmdFlags) error {
	return webui.Run(webui.Config{
		Addr:     c.addr,
		Session:  c.session,
		Root:     c.root,
		Token:    c.token,
		ReadOnly: c.readOnly,
	})
}
