package paths

import "testing"

func TestFolderName(t *testing.T) {
	cases := map[string]string{"/": "root", "/a/b/": "b", "/home/b": "b", "": "", ".": "root"}
	for dir, want := range cases {
		if got := FolderName(dir); got != want {
			t.Fatalf("FolderName(%q) = %q, want %q", dir, got, want)
		}
	}
}

// These tests pin StateDir's env precedence: DM_STATE, then
// XDG_STATE_HOME, then home fallback. They mutate process env via
// t.Setenv, so they cannot run in parallel; keep them serial.
func TestStateDirDMStateWins(t *testing.T) {
	t.Setenv("DM_STATE", "/data/dm-state")
	t.Setenv("XDG_STATE_HOME", "/xdg")
	if got := StateDir(); got != "/data/dm-state" {
		t.Fatalf("StateDir() = %q, want /data/dm-state", got)
	}
}

func TestStateDirXDGStateHome(t *testing.T) {
	t.Setenv("DM_STATE", "")
	t.Setenv("XDG_STATE_HOME", "/xdg")
	if got := StateDir(); got != "/xdg/dm" {
		t.Fatalf("StateDir() = %q, want /xdg/dm", got)
	}
}

func TestStateDirHomeFallback(t *testing.T) {
	t.Setenv("DM_STATE", "")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", "/home/tester")
	if got := StateDir(); got != "/home/tester/.local/state/dm" {
		t.Fatalf("StateDir() = %q, want /home/tester/.local/state/dm", got)
	}
}

func TestSessionPaths(t *testing.T) {
	t.Setenv("DM_STATE", "/st")
	t.Setenv("XDG_STATE_HOME", "")
	want := map[string]string{
		SessionDir("s"):    "/st/sessions/s",
		ServerSock("s"):    "/st/sessions/s/server.sock",
		PaneSock("s", "7"): "/st/sessions/s/panes/7.sock",
		StatePath("s"):     "/st/sessions/s/state.json",
	}
	for got, exp := range want {
		if got != exp {
			t.Fatalf("got %q, want %q", got, exp)
		}
	}
}

func TestFolderNameSeparators(t *testing.T) {
	cases := map[string]string{"./a": "a", "a/..": "root", "relative": "relative", "/a": "a"}
	for dir, want := range cases {
		if got := FolderName(dir); got != want {
			t.Fatalf("FolderName(%q) = %q, want %q", dir, got, want)
		}
	}
}

func TestSessionPathsAt(t *testing.T) {
	t.Parallel()
	cases := []struct {
		root, name, id string
		want           map[string]string
	}{
		{
			root: "/st", name: "s", id: "7",
			want: map[string]string{
				SessionDirAt("/st", "s"):    "/st/sessions/s",
				ServerSockAt("/st", "s"):    "/st/sessions/s/server.sock",
				PaneSockAt("/st", "s", "7"): "/st/sessions/s/panes/7.sock",
				StatePathAt("/st", "s"):     "/st/sessions/s/state.json",
			},
		},
		{
			root: "/other/root", name: "session-one", id: "pane-1",
			want: map[string]string{
				SessionDirAt("/other/root", "session-one"):         "/other/root/sessions/session-one",
				ServerSockAt("/other/root", "session-one"):         "/other/root/sessions/session-one/server.sock",
				PaneSockAt("/other/root", "session-one", "pane-1"): "/other/root/sessions/session-one/panes/pane-1.sock",
				StatePathAt("/other/root", "session-one"):          "/other/root/sessions/session-one/state.json",
			},
		},
	}
	for _, tc := range cases {
		for got, exp := range tc.want {
			if got != exp {
				t.Fatalf("root %q: got %q, want %q", tc.root, got, exp)
			}
		}
	}
}
