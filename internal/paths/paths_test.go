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
