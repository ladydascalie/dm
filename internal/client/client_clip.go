package client

import (
	"encoding/base64"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// clipProbeWait is how long the client waits for an OSC 52 read reply before
// it counts the outer terminal as silent. A terminal that answers the query
// does so within one round trip; a terminal that drops the sequence answers
// never.
const clipProbeWait = 300 * time.Millisecond

// clipMode is what the client does with clipboard writes. clipUnknown is the
// probe still open, and a write in that state rides OSC 52: a terminal that
// drops the sequence drops it silently, and the probe settles soon after.
type clipMode int

const (
	clipUnknown clipMode = iota
	clipOSC52
	clipExternal
)

// clipWrite is the settled clipboard mode for the process. The probe runs
// once per attach and session switches keep its answer, so it sits at
// package scope the way the theme depth does.
var (
	clipWrite   clipMode = clipUnknown
	clipProbeAt time.Time
)

// osc52Seq frames one OSC 52 body as a terminal sequence. Under tmux it must
// ride the passthrough DCS with doubled escapes, or tmux swallows it.
func osc52Seq(data string) string {
	seq := "\x1b]52;c;" + data + "\x1b\\"
	if os.Getenv("TMUX") != "" {
		return "\x1bPtmux;" + strings.ReplaceAll(seq, "\x1b", "\x1b\x1b") + "\x1b\\"
	}
	return seq
}

// osc52 is the clipboard write sequence for text.
func osc52(text string) string {
	return osc52Seq(base64.StdEncoding.EncodeToString([]byte(text)))
}

// clipQuery is the OSC 52 read probe. A terminal that honours OSC 52 replies
// with its clipboard; one that does not stays silent. Silence is not proof of
// a deaf write path, because a terminal may honour writes and still refuse
// the read, but the read is the only handshake OSC 52 offers.
func clipQuery() string {
	return osc52Seq("?")
}

// noteClipReply records a probe reply: the outer terminal speaks OSC 52.
func noteClipReply() {
	clipWrite = clipOSC52
}

// clipResolve settles the probe once its window passes with no reply, and
// returns the mode a clipboard write should use now.
func clipResolve(now time.Time) clipMode {
	if clipWrite == clipUnknown && !clipProbeAt.IsZero() && now.Sub(clipProbeAt) > clipProbeWait {
		clipWrite = clipExternal
	}
	return clipWrite
}

// clipTool is one external clipboard carrier: the binary and the arguments
// that put text on the clipboard.
type clipTool struct {
	name string
	args []string
}

// clipTools lists the carriers the host actually has, in preference order.
// wl-copy beats xclip because a Wayland session has no X clipboard for xclip
// to write. clip.exe carries the clipboard on WSL, where it sits on PATH and
// no X tool exists.
func clipTools(goos string, lookPath func(string) (string, error)) []clipTool {
	var all []clipTool
	switch goos {
	case "darwin":
		all = []clipTool{{name: "pbcopy"}}
	case "windows":
		all = []clipTool{{name: "clip.exe"}}
	default:
		all = []clipTool{{name: "wl-copy"}, {name: "xclip", args: []string{"-selection", "clipboard"}}, {name: "clip.exe"}}
	}
	var out []clipTool
	for _, tool := range all {
		if _, err := lookPath(tool.name); err == nil {
			out = append(out, tool)
		}
	}
	return out
}

// writeClipboard sends text to the terminal's clipboard. A terminal the probe
// heard stays on OSC 52; one it did not gets the text piped to a platform
// tool instead.
func (t *tui) writeClipboard(text string) {
	if clipResolve(time.Now()) != clipExternal {
		_, _ = os.Stdout.WriteString(osc52(text))
		return
	}
	tools := clipTools(runtime.GOOS, exec.LookPath)
	if len(tools) == 0 {
		t.notify("no clipboard carrier; install pbcopy, wl-copy or xclip")
		return
	}
	cmd := exec.Command(tools[0].name, tools[0].args...)
	cmd.Stdin = strings.NewReader(text)
	if err := cmd.Run(); err != nil {
		t.notify("clipboard: " + err.Error())
	}
}
