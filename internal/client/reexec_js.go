//go:build js

package client

import "errors"

// reexecSelf has no meaning in a browser tab: there is no binary on disk to
// hand the process over to. The page that mounts the client reconnects on its
// own terms, so a restart ends the attach the way a detach does.
func reexecSelf(string) error {
	return errors.New("restart: a browser tab has no binary to exec")
}
