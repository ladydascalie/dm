package client

import (
	"encoding/json"
	"testing"
	"time"

	"dm/internal/keymap"
	"dm/internal/testutil"
	"dm/internal/wire"
)

// TestPluginKeySendsRunPlugin: a plugin: action asks the server to open the
// declared plugin beside the focused pane, with the name and the key source in
// the payload. The server owns the lookup and the argv, so the client sends
// no command text.
func TestPluginKeySendsRunPlugin(t *testing.T) {
	t.Parallel()
	tui, got := switcherTUI(t)
	cfg, err := keymap.ParseConfig(`plugin "rail" {
	exec "/bin/true"
	where "split"
}
keymap "main" {
	"plugin:rail" "g"
}`)
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	tui.keymap = cfg

	// The prefix arms the main keymap; g then dispatches the plugin.
	feed(tui, "\x02g")
	var req wire.Msg
	select {
	case m := <-got:
		req = m
	case <-time.After(time.Second):
		t.Fatal("g sent no run-plugin request")
	}
	if req.Type != "req" || req.Action != "run-plugin" || req.Target != "w1p1" {
		t.Fatalf("g sent %+v, want a run-plugin request for the focused pane w1p1", req)
	}
	var res struct {
		Name   string `json:"name"`
		Source string `json:"source"`
		Where  string `json:"where"`
	}
	if err := json.Unmarshal(req.Res, &res); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if res.Name != "rail" || res.Source != "key" || res.Where != "" {
		t.Fatalf("payload = %+v, want the name, the key source and no placement override", res)
	}
	testutil.NoMsg(t, got)
}
