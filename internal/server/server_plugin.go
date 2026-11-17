package server

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"dm/internal/keymap"
	"dm/internal/layout"
	"dm/internal/paths"
	"dm/internal/session"
)

// pluginContext is the invocation a plugin pane learns at launch. Every field
// is optional: a launch that cannot resolve one leaves it out.
type pluginContext struct {
	Session   string         `json:"session,omitempty"`
	Source    string         `json:"source,omitempty"`
	Plugin    string         `json:"plugin,omitempty"`
	Workspace *pluginRef     `json:"workspace,omitempty"`
	Window    *pluginRef     `json:"window,omitempty"`
	Pane      *pluginPaneRef `json:"pane,omitempty"`
}

type pluginRef struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

type pluginPaneRef struct {
	ID     string `json:"id,omitempty"`
	Cwd    string `json:"cwd,omitempty"`
	Agent  string `json:"agent,omitempty"`
	App    string `json:"app,omitempty"`
	Status string `json:"status,omitempty"`
}

// plugin returns the declaration named name from the config the server
// holds. A reloaded config is a new pointer, so the read takes the lock and
// works from the pointer it took.
func (s *server) plugin(name string) (keymap.Plugin, bool) {
	s.mu.Lock()
	cfg := s.cfg
	s.mu.Unlock()
	if cfg == nil {
		return keymap.Plugin{}, false
	}
	return cfg.Plugin(name)
}

// plugins returns every declaration in the config the server holds.
func (s *server) plugins() []keymap.Plugin {
	s.mu.Lock()
	cfg := s.cfg
	s.mu.Unlock()
	if cfg == nil {
		return nil
	}
	return cfg.Plugins()
}

// runPlugin opens one plugin pane: the argv the declaration carries, in the
// folder it names, at the demanded placement. A float is refused because a
// float ends with the client that opened it.
func (s *server) runPlugin(p keymap.Plugin, target, where, source string) (string, error) {
	env, err := s.pluginEnv(p, target, source)
	if err != nil {
		return "", err
	}
	switch where {
	case "split":
		return s.split(target, "right", p.Cwd, 0, false, p.Exec, env)
	case "window":
		_, id, err := s.newWindow(target, "", p.Cwd, false, p.Exec, env)
		return id, err
	case "float":
		return "", fmt.Errorf("plugin %q: a float ends with its client, so it cannot host a plugin", p.Name)
	default:
		return "", fmt.Errorf("plugin %q: where must be split or window, got %q", p.Name, where)
	}
}

// pluginEnv creates the plugin's state directory and builds the environment
// its pane adds: the block's entries first, then the host's own names. The
// parser refuses a DM_ key in the block, so the host cannot be shadowed.
func (s *server) pluginEnv(p keymap.Plugin, target, source string) ([]string, error) {
	s.mu.Lock()
	ctx := pluginContext{Session: s.st.Name, Source: source, Plugin: p.Name}
	ws, win, id := s.pluginSourceLocked(target)
	if ws != nil {
		ctx.Workspace = &pluginRef{ID: ws.ID, Name: ws.Name}
	}
	if win != nil {
		ctx.Window = &pluginRef{ID: win.ID, Name: win.Name}
	}
	if id != "" {
		ref := &pluginPaneRef{ID: id}
		if ps := s.st.Panes[id]; ps != nil {
			ref.Cwd, ref.Agent, ref.App = ps.Cwd, ps.Name, ps.App
		}
		if pc := s.panes[id]; pc != nil {
			ref.Status = pc.status().State
		}
		ctx.Pane = ref
	}
	s.mu.Unlock()

	dir := filepath.Join(paths.StateDir(), "plugins", p.Name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("plugin %q: state directory: %w", p.Name, err)
	}
	raw, err := json.Marshal(ctx)
	if err != nil {
		return nil, fmt.Errorf("plugin %q: context: %w", p.Name, err)
	}
	env := make([]string, 0, len(p.Env)+4)
	env = append(env, p.Env...)
	env = append(env,
		"DM_PLUGIN_ID="+p.Name,
		"DM_PLUGIN_ROOT="+filepath.Dir(p.Exec[0]),
		"DM_PLUGIN_STATE_DIR="+dir,
		"DM_PLUGIN_CONTEXT_JSON="+string(raw),
	)
	return env, nil
}

// pluginSourceLocked returns the workspace, window and pane a launch acts on.
// A target that names a pane wins; otherwise the active window's focus is the
// source. A target naming no pane leaves the context without a pane.
func (s *server) pluginSourceLocked(target string) (*session.Workspace, *session.Window, string) {
	if len(s.st.Workspaces) == 0 {
		return nil, nil, ""
	}
	active := s.st.ActiveWindow()
	for _, ws := range s.st.Workspaces {
		for _, w := range ws.Windows {
			switch {
			case target != "" && layout.FindLeaf(w.Root, target):
				return ws, w, target
			case target == "" && w == active:
				return ws, w, w.Focus
			}
		}
	}
	return nil, nil, ""
}
