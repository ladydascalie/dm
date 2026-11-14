// Package browser opens a target in the system's default browser.
package browser

import (
	"os/exec"
	"runtime"
)

// Open launches the system browser for target, which is a URL or a file path.
// It starts the opener and returns; a goroutine reaps the opener once it
// exits, so the caller holds no zombie. An error means no opener could start,
// so the caller still owns target.
func Open(target string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	// Reap the opener in the background. The caller is a long-lived TUI, so
	// a child nobody waits for stays a zombie until the client exits.
	go func() { _ = cmd.Wait() }()
	return nil
}
