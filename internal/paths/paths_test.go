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
