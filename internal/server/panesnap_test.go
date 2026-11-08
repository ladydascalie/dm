package server

import (
	"testing"

	"dm/internal/vt"
)

// newScreenSnapshot is the picture a fake pane sends for a screen request.
func newScreenSnapshot(id string) *vt.Snapshot {
	term := vt.NewTerm(16, 2)
	term.Write([]byte("screen " + id))
	return term.Snapshot()
}

// snapshotBytes is the wire form used to compare a forwarded snapshot with the
// one the pane sent.
func snapshotBytes(t *testing.T, s *vt.Snapshot) []byte {
	t.Helper()
	if s == nil {
		t.Fatal("nil snapshot")
	}
	b, err := s.Encode()
	if err != nil {
		t.Fatalf("encode snapshot: %v", err)
	}
	return b
}
