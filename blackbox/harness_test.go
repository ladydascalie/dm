package blackbox

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"dm/internal/keymap"
)

// buildOnce builds the dm binary under test. The test package lives one
// directory below the module root, so the build runs with Dir set to "..".
func buildOnce(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "dm")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	cmd.Dir = ".."
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return bin
}

// testEnv is the parent environment with every dm and XDG override removed,
// so a test owns its state and config roots. A duplicate key is a trap: exec
// keeps the last value while a reader that scans the slice finds the first.
func testEnv(base []string, extra ...string) []string {
	drop := map[string]bool{
		"DM_STATE":        true,
		"DM_SESSION":      true,
		"DM_PANE":         true,
		"DM_NOTIFY":       true,
		"DM_NERD":         true,
		"DM_STATS":        true,
		"DM_COLOR":        true,
		"XDG_STATE_HOME":  true,
		"XDG_CONFIG_HOME": true,
	}
	env := make([]string, 0, len(base)+len(extra))
	for _, e := range base {
		if k, _, ok := strings.Cut(e, "="); ok && drop[k] {
			continue
		}
		env = append(env, e)
	}
	return append(env, extra...)
}

type harness struct {
	t   *testing.T
	bin string
	env []string
}

func newHarness(t *testing.T) *harness {
	state, err := os.MkdirTemp("", "dmt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Stop panes and servers this test spawned.
		_ = filepath.Walk(state, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(p, ".pid") {
				return nil
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return nil
			}
			var pid int
			_, _ = fmt.Sscanf(string(b), "%d", &pid)
			if pid > 0 {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
			return nil
		})
		_ = os.RemoveAll(state)
	})
	cfgDir, err := os.MkdirTemp("", "dmcfg")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(cfgDir) })
	return &harness{t: t, bin: buildOnce(t), env: testEnv(os.Environ(), "DM_STATE="+state, "XDG_CONFIG_HOME="+cfgDir)}
}

func (h *harness) run(args ...string) string {
	h.t.Helper()
	cmd := exec.Command(h.bin, args...)
	cmd.Env = h.env
	out, err := cmd.CombinedOutput()
	if err != nil {
		h.t.Fatalf("dm %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func (h *harness) try(args ...string) (string, error) {
	cmd := exec.Command(h.bin, args...)
	cmd.Env = h.env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (h *harness) rpc(session string, v map[string]any) map[string]any {
	h.t.Helper()
	raw, _ := json.Marshal(v)
	out := h.run("exec", string(raw), "-s", session)
	var res map[string]any
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		h.t.Fatalf("bad exec output %q: %v", out, err)
	}
	return res
}

func (h *harness) stateDir() string {
	for _, e := range h.env {
		if strings.HasPrefix(e, "DM_STATE=") {
			return strings.TrimPrefix(e, "DM_STATE=")
		}
	}
	return ""
}

func (h *harness) panes(session string) []string {
	h.t.Helper()
	tree := h.rpc(session, map[string]any{"action": "list"})
	snap := tree["snapshot"].(map[string]any)
	var ids []string
	for _, raw := range snap["panes"].([]any) {
		ids = append(ids, raw.(map[string]any)["id"].(string))
	}
	return ids
}

func (h *harness) focus(session string) string {
	h.t.Helper()
	tree := h.rpc(session, map[string]any{"action": "list"})
	return tree["snapshot"].(map[string]any)["focus"].(string)
}

func (h *harness) split(session, target, dir string) string {
	h.t.Helper()
	res := h.rpc(session, map[string]any{"action": "split", "target": target, "dir": dir})
	id, _ := res["pane"].(string)
	if id == "" {
		h.t.Fatalf("split returned no pane: %v", res)
	}
	return id
}

// waitBudget bounds how long waitFor polls for a needle. A pane runs the
// operator's login shell, so its startup (zshrc, git status, plugin
// bootstraps) delays the first command: input typed before the shell reads it
// waits in the PTY, and the output lands late. Budget for a cold shell start.
const waitBudget = 30 * time.Second

func (h *harness) waitFor(session, pane, needle string) string {
	h.t.Helper()
	start := time.Now()
	deadline := start.Add(waitBudget)
	var last string
	for time.Now().Before(deadline) {
		out, err := h.try("read", pane, "-s", session)
		last = out
		if err == nil && strings.Contains(out, needle) {
			return out
		}
		time.Sleep(100 * time.Millisecond)
	}
	h.t.Fatalf("pane %s never showed %q within the %s wait budget (waited %s); last read:\n%s",
		pane, needle, waitBudget, time.Since(start).Round(time.Millisecond), last)
	return last
}

func (h *harness) waitPanes(session string, n int) {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		tree := h.rpc(session, map[string]any{"action": "list"})
		snap := tree["snapshot"].(map[string]any)
		if len(snap["panes"].([]any)) == n {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	h.t.Fatalf("session %s never reached %d panes", session, n)
}

func (h *harness) history(session, pane string, n int) []string {
	h.t.Helper()
	res := h.rpc(session, map[string]any{"action": "history", "target": pane, "count": n})
	raw, _ := res["lines"].([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		line, _ := v.(string)
		out = append(out, line)
	}
	return out
}

func (h *harness) rects(session string) map[string]paneRect {
	h.t.Helper()
	tree := h.rpc(session, map[string]any{"action": "list"})
	snap := tree["snapshot"].(map[string]any)
	out := map[string]paneRect{}
	for _, raw := range snap["panes"].([]any) {
		p := raw.(map[string]any)
		rr := p["rect"].(map[string]any)
		num := func(k string) int { return int(rr[k].(float64)) }
		out[p["id"].(string)] = paneRect{X: num("X"), Y: num("Y"), W: num("W"), H: num("H")}
	}
	return out
}

// sessionState reads the server's persisted state for a harness session.
func (h *harness) sessionState(name string) *stateFile {
	h.t.Helper()
	b, err := os.ReadFile(filepath.Join(h.stateDir(), "sessions", name, "state.json"))
	if err != nil {
		h.t.Fatalf("state file: %v", err)
	}
	var st stateFile
	if err := json.Unmarshal(b, &st); err != nil {
		h.t.Fatalf("state json: %v", err)
	}
	return &st
}

// zoom reads the session's zoomed pane id, empty when nothing is zoomed.
func (h *harness) zoom(session string) string {
	h.t.Helper()
	tree := h.rpc(session, map[string]any{"action": "list"})
	zoom, _ := tree["snapshot"].(map[string]any)["zoom"].(string)
	return zoom
}

// toggleZoom runs the zoom-pane action and returns the pane id left zoomed.
func (h *harness) toggleZoom(session string) string {
	h.t.Helper()
	res := h.rpc(session, map[string]any{"action": "zoom-pane"})
	zoom, _ := res["zoom"].(string)
	return zoom
}

// paneAreaOf is the session's pane area: the box a single unsplit pane fills.
func (h *harness) paneAreaOf(session string) paneRect {
	h.t.Helper()
	tree := h.rpc(session, map[string]any{"action": "list"})
	snap := tree["snapshot"].(map[string]any)
	num := func(k string) int { return int(snap[k].(float64)) }
	return paneAreaFor(num("cols"), num("rows"), num("rail"))
}

func readPID(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("pid file %s: %v", path, err)
	}
	var pid int
	if _, err := fmt.Sscanf(string(b), "%d", &pid); err != nil {
		t.Fatalf("pid file %s: %v", path, err)
	}
	return pid
}

func waitMode(t *testing.T, h *harness, session string, alt, mouse bool) {
	t.Helper()
	start := time.Now()
	deadline := start.Add(waitBudget)
	for {
		tree := h.rpc(session, map[string]any{"action": "list"})
		snap := tree["snapshot"].(map[string]any)
		for _, raw := range snap["panes"].([]any) {
			p := raw.(map[string]any)
			gotAlt, _ := p["alt"].(bool)
			gotMouse, _ := p["mouse"].(bool)
			if gotAlt == alt && gotMouse == mouse {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("pane never reached alt=%v mouse=%v within the %s wait budget (waited %s): %v",
				alt, mouse, waitBudget, time.Since(start).Round(time.Millisecond), snap["panes"])
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// stateFile is the part of the session state file these tests read back. It is
// local on purpose: the harness proves what the file records without naming a
// production type.
type stateFile struct {
	Name          string                `json:"name"`
	Active        int                   `json:"active"`
	NextWorkspace int                   `json:"nextWorkspace"`
	NextWindow    int                   `json:"nextWindow"`
	Cols          int                   `json:"cols"`
	Rows          int                   `json:"rows"`
	Workspaces    []*stateWorkspace     `json:"workspaces"`
	Panes         map[string]*statePane `json:"panes"`
}

type stateWorkspace struct {
	ID      string         `json:"id"`
	Name    string         `json:"name"`
	Active  int            `json:"active"`
	Windows []*stateWindow `json:"windows"`
}

type stateWindow struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Focus    string `json:"focus"`
	Unfolded bool   `json:"unfolded"`
}

type statePane struct {
	Cwd      string `json:"cwd"`
	Cols     int    `json:"cols"`
	Rows     int    `json:"rows"`
	Reported bool   `json:"reported"`
	App      string `json:"app"`
}

// paneRect is the rectangle the snapshot reports for a pane. Its field names
// match the wire value, so it decodes straight from the JSON.
type paneRect struct {
	X, Y, W, H int
}

// The rail and pane-area geometry the moved tests assert on. A black-box test
// cannot call the production layout functions, so these mirror state.go. Keep
// them in step with it.
const defaultRail = 34

func maxRail(cols int) int {
	m := cols / 3
	if m > 46 {
		m = 46
	}
	if m < 0 {
		m = 0
	}
	return m
}

func effectiveRail(cols, rail int) int {
	if rail < 0 {
		rail = 0
	}
	if m := maxRail(cols); rail > m {
		rail = m
	}
	return rail
}

func paneAreaFor(cols, rows, rail int) paneRect {
	rail = effectiveRail(cols, rail)
	w := cols - rail
	if w < 1 {
		w = 1
	}
	return paneRect{rail, 1, w, rows - 2}
}

// pad is the pane padding the default config asks for, so the harness geometry
// follows the same rule the server lays out with.
func pad() int { return keymap.LoadDefault().Padding() }

func insetBox(b paneRect) paneRect {
	if b.W < 3 || b.H < 3 {
		return paneRect{X: b.X, Y: b.Y, W: maxInt(1, b.W), H: maxInt(1, b.H)}
	}
	inset := 1 + pad()
	if b.W < 2*inset+1 || b.H < 2*inset+1 {
		inset = 1
	}
	return paneRect{X: b.X + inset, Y: b.Y + inset, W: b.W - 2*inset, H: b.H - 2*inset}
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// folderName mirrors state.go: a window takes the name of its folder.
func folderName(dir string) string {
	if dir == "" {
		return ""
	}
	dir = filepath.Clean(dir)
	base := filepath.Base(dir)
	if base == "/" || base == "." || base == string(filepath.Separator) {
		return "root"
	}
	return base
}

// paneMsg is the frame these tests exchange with a pane daemon. Only the
// fields the screen request uses are kept. It mirrors proto.go's Msg, because
// the pane wire format is part of the contract the test drives.
type paneMsg struct {
	Type   string          `json:"type"`
	Action string          `json:"action,omitempty"`
	ID     int             `json:"id,omitempty"`
	Data   []byte          `json:"data,omitempty"`
	VTSnap json.RawMessage `json:"vtsnap,omitempty"`
	Cols   int             `json:"cols,omitempty"`
	Rows   int             `json:"rows,omitempty"`
}

// paneRequest asks one pane daemon for its screen bytes. It mirrors
// readPaneScreen in server.go.
func paneRequest(state, session, id string, m paneMsg) (paneMsg, error) {
	var out paneMsg
	sock := filepath.Join(state, "sessions", session, "panes", id+".sock")
	c, err := net.DialTimeout("unix", sock, time.Second)
	if err != nil {
		return out, err
	}
	defer func() { _ = c.Close() }()
	m.ID = 1
	if err := writePaneMsg(c, m); err != nil {
		return out, err
	}
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	for {
		got, err := readPaneMsg(c)
		if err != nil {
			return out, err
		}
		if got.Type == "resp" || got.Type == "replay" || got.Type == "screen" {
			return got, nil
		}
	}
}

const maxPaneFrame = 16 << 20

func writePaneMsg(w io.Writer, m paneMsg) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	var h [4]byte
	binary.BigEndian.PutUint32(h[:], uint32(len(b)))
	if _, err := w.Write(h[:]); err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

func readPaneMsg(r io.Reader) (paneMsg, error) {
	var m paneMsg
	var h [4]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return m, err
	}
	n := binary.BigEndian.Uint32(h[:])
	if n == 0 || n > maxPaneFrame {
		return m, fmt.Errorf("bad frame length %d", n)
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, errors.New("bad json: " + err.Error())
	}
	return m, nil
}
