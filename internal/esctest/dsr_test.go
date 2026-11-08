package esctest

import (
	"io"
	"os"
	"testing"

	"dm/internal/vt"

	"github.com/hinshun/vt10x"
)

// dsrHarness returns a harness whose reports go to a pipe, plus the read end.
// The caller closes the write end after feeding and reads the replies.
func dsrHarness(t *testing.T) (*harness, *os.File, *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	t.Cleanup(func() {
		_ = r.Close()
		_ = w.Close()
	})
	h := &harness{
		ptmx:    w,
		vt:      vt10x.New(vt10x.WithSize(80, 25), vt10x.WithWriter(io.Discard)),
		reports: vt.NewTerm(80, 25),
		errw:    io.Discard,
	}
	h.st = newState(25, 80)
	return h, r, w
}

// feedRead feeds in and returns everything the harness replied with.
func feedRead(t *testing.T, h *harness, r, w *os.File, in string) string {
	t.Helper()
	h.feed([]byte(in))
	if err := w.Close(); err != nil {
		t.Fatalf("close pipe: %v", err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read reply: %v", err)
	}
	return string(got)
}

// TestPlainDSR6ReachesTheVT pins that a plain CSI 6 n is routed to the report
// terminal and its cursor reply comes back to the host.
func TestPlainDSR6ReachesTheVT(t *testing.T) {
	h, r, w := dsrHarness(t)
	h.feed([]byte("\x1b[3;4H"))
	got := feedRead(t, h, r, w, "\x1b[6n")
	if want := "\x1b[3;4R"; got != want {
		t.Fatalf("reply = %q, want %q", got, want)
	}
}

// TestDECXCPRIsUnchanged pins that CSI ? 6 n still answers the private form.
func TestDECXCPRIsUnchanged(t *testing.T) {
	h, r, w := dsrHarness(t)
	h.feed([]byte("\x1b[3;4H"))
	got := feedRead(t, h, r, w, "\x1b[?6n")
	if want := "\x1b[?3;4;1R"; got != want {
		t.Fatalf("reply = %q, want %q", got, want)
	}
}

// TestHarnessDSRAnswersAreUnchanged pins the private DSR numbers the harness
// still answers itself.
func TestHarnessDSRAnswersAreUnchanged(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"printer port", "\x1b[?15n", "\x1b[?10n"},
		{"UDK", "\x1b[?25n", "\x1b[?20n"},
		{"keyboard", "\x1b[?26n", "\x1b[?27;1;0;0n"},
		{"locator", "\x1b[?55n", "\x1b[?50n"},
		{"locator type", "\x1b[?56n", "\x1b[?57;0n"},
		{"DECMSR", "\x1b[?62n", "\x1b[0*{"},
		{"DECCKSR", "\x1b[?63;42n", "\x1bP42!~0000\x1b\\"},
		{"data integrity", "\x1b[?75n", "\x1b[?70n"},
		{"multi-session", "\x1b[?85n", "\x1b[?83n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, r, w := dsrHarness(t)
			got := feedRead(t, h, r, w, tt.in)
			if got != tt.want {
				t.Fatalf("%q reply = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
