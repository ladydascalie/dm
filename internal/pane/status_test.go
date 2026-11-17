package pane

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"

	"dm/internal/wire"
)

// statusValue is the stub value these tests register. It is not a live
// secret: nothing here leaves the test binary.
const statusValue = "poc-status-1234"

// statusPane is a pane that holds statusValue on its redaction list: the
// harness pane from yankPane, with the set pushed over its own socket, so the
// wire form is exercised too.
func statusPane(t *testing.T) (*paneProc, net.Conn, chan wire.Msg) {
	t.Helper()
	p, cli, msgs := yankPane(t, 60, 10)
	// The adopted signal is the daemon's, and this harness builds the pane by
	// hand: the push would close a nil channel.
	p.adopted = make(chan struct{})
	body := jsonRaw(map[string]any{
		"values":    []string{statusValue},
		"valuesB64": [][]byte{[]byte(statusValue)},
	})
	reply := paneRequest(t, cli, msgs, wire.Msg{Type: "req", Action: "set-secrets", ID: 1, Res: body})
	if !reply.OK {
		t.Fatalf("set-secrets: %s", reply.Error)
	}
	return p, cli, msgs
}

// feedStatus runs one OSC 7501 report through the pane's real filter, the
// path the pty's bytes take, and returns what the filter passed through.
func feedStatus(t *testing.T, p *paneProc, report string, chunks int) string {
	t.Helper()
	f := p.newFilter()
	var out bytes.Buffer
	// A report split across reads is the filter's partial-sequence buffer:
	// the value must not need the whole sequence in one read.
	n := (len(report) + chunks - 1) / chunks
	for i := 0; i < len(report); i += n {
		f.Feed([]byte(report[i:min(i+n, len(report))]), &out)
	}
	return out.String()
}

// wireStatus renders the base64 wire format the pane speaks:
// state=error:app=tool:msg=<b64>.
func wireStatus(state, app, message string) string {
	s := "state=" + state
	if app != "" {
		s += ":app=" + app
	}
	if message != "" {
		s += ":msg=" + base64.StdEncoding.EncodeToString([]byte(message))
	}
	return "\x1b]7501;" + s + "\x07"
}

// TestStatusMessageIsScrubbed proves the status report's message never reaches
// a read surface raw. The filter decodes the report out of the byte stream, so
// the stage never sees it: the daemon scrubs the decoded text before it stores
// or publishes the status, and every later reader draws the redaction. The
// state itself is still recorded, so a report that carried a value settles a
// wait the same way a clean one does.
func TestStatusMessageIsScrubbed(t *testing.T) {
	t.Parallel()
	p, cli, msgs := statusPane(t)
	sub := subscriber(p)

	filtered := feedStatus(t, p, wireStatus("error", "tool", "auth failed: "+statusValue), 1)
	if filtered != "" {
		t.Fatalf("the report leaked into the stream: %q", filtered)
	}

	p.mu.Lock()
	st := p.st
	p.mu.Unlock()
	want := "auth failed: " + strings.Repeat("*", len(statusValue))
	if st.State != "error" {
		t.Fatalf("state = %q, want error: the wait machinery needs it", st.State)
	}
	if st.Message != want {
		t.Fatalf("stored message = %q, want %q", st.Message, want)
	}
	if strings.Contains(st.Message, statusValue) {
		t.Fatalf("the stored message holds the value: %q", st.Message)
	}

	// The broadcast the server receives, and the frames a read draws from.
	frame := waitStatusFrame(t, sub)
	if frame.St == nil || frame.St.Message != want {
		t.Fatalf("broadcast status = %+v, want the redaction", frame.St)
	}
	if strings.Contains(string(mustJSON(t, frame)), statusValue) {
		t.Fatalf("the broadcast frame holds the value: %+v", frame)
	}
	reply := paneRequest(t, cli, msgs, wire.Msg{Type: "req", Action: "status", ID: 2})
	if reply.St == nil || reply.St.Message != want {
		t.Fatalf("status reply = %+v, want the redaction", reply.St)
	}
}

// TestStatusMessageIsScrubbedAcrossReads proves the filter's partial-sequence
// buffer is enough: a report split over many reads still decodes once and is
// scrubbed, so a program cannot dodge the stage by writing the sequence in
// pieces.
func TestStatusMessageIsScrubbedAcrossReads(t *testing.T) {
	t.Parallel()
	p, _, _ := statusPane(t)
	feedStatus(t, p, wireStatus("blocked", "tool", statusValue), 7)
	p.mu.Lock()
	st := p.st
	p.mu.Unlock()
	if strings.Contains(st.Message, statusValue) {
		t.Fatalf("a split report published the value: %q", st.Message)
	}
	if st.Message != strings.Repeat("*", len(statusValue)) {
		t.Fatalf("message = %q, want the redaction", st.Message)
	}
	if st.State != "blocked" {
		t.Fatalf("state = %q, want blocked", st.State)
	}
}

// TestStatusJSONFormIsScrubbed proves the older JSON form takes the same path:
// the value is decoded before the stage would see it, so the scrub has to run
// on the decoded text whichever spelling carried it.
func TestStatusJSONFormIsScrubbed(t *testing.T) {
	t.Parallel()
	p, _, _ := statusPane(t)
	body, err := json.Marshal(wire.Status{State: "done", Message: "token " + statusValue})
	if err != nil {
		t.Fatal(err)
	}
	feedStatus(t, p, "\x1b]7501;"+string(body)+"\x07", 1)
	p.mu.Lock()
	st := p.st
	p.mu.Unlock()
	if strings.Contains(st.Message, statusValue) {
		t.Fatalf("the JSON form published the value: %q", st.Message)
	}
	if st.State != "done" {
		t.Fatalf("state = %q, want done", st.State)
	}
}

// TestStatusAppIsScrubbed proves the app name is scrubbed too: it is persisted
// in the state file and drawn in the rail, and the report can carry a value in
// it just as easily.
func TestStatusAppIsScrubbed(t *testing.T) {
	t.Parallel()
	p, _, _ := statusPane(t)
	feedStatus(t, p, wireStatus("working", statusValue, ""), 1)
	p.mu.Lock()
	st := p.st
	p.mu.Unlock()
	if strings.Contains(st.App, statusValue) {
		t.Fatalf("the app name kept the value: %q", st.App)
	}
	if st.App != strings.Repeat("*", len(statusValue)) {
		t.Fatalf("app = %q, want the redaction", st.App)
	}
}

// TestStatusStateNeverCarriesAValue proves the state field is validated, not
// scrubbed: every verb prints the state unscrubbed, so a report cannot put a
// value there at all. The pane holds the value on its list, so an asterisk
// state here would mean the wrong fix ran.
func TestStatusStateNeverCarriesAValue(t *testing.T) {
	t.Parallel()
	p, _, _ := statusPane(t)
	feedStatus(t, p, "\x1b]7501;state="+statusValue+":app=pi\x07", 1)
	p.mu.Lock()
	st := p.st
	p.mu.Unlock()
	if st.State != "unknown" {
		t.Fatalf("state = %q, want unknown: a state is not free text", st.State)
	}
}

// TestStatusRescrubbedWhenTheSetArrives proves a status stored before the value
// list existed does not stay raw. The grace lets a shell start with no list, so
// a report can land first; the set has to reach the stored text and the clients
// that read it.
func TestStatusRescrubbedWhenTheSetArrives(t *testing.T) {
	t.Parallel()
	p, cli, msgs := yankPane(t, 60, 10)
	// The adopted signal is the daemon's, and this harness builds the pane by
	// hand: the push would close a nil channel.
	p.adopted = make(chan struct{})
	// No list yet: the report is stored as it arrived.
	feedStatus(t, p, wireStatus("error", statusValue, "auth failed: "+statusValue), 1)
	p.mu.Lock()
	st := p.st
	p.mu.Unlock()
	if st.Message != "auth failed: "+statusValue || st.App != statusValue {
		t.Fatalf("status = %+v, want it stored as reported", st)
	}

	sub := subscriber(p)
	body := jsonRaw(map[string]any{
		"values":    []string{statusValue},
		"valuesB64": [][]byte{[]byte(statusValue)},
	})
	if reply := paneRequest(t, cli, msgs, wire.Msg{Type: "req", Action: "set-secrets", ID: 1, Res: body}); !reply.OK {
		t.Fatalf("set-secrets: %s", reply.Error)
	}

	want := "auth failed: " + strings.Repeat("*", len(statusValue))
	p.mu.Lock()
	st = p.st
	p.mu.Unlock()
	if st.Message != want {
		t.Fatalf("stored message = %q, want %q", st.Message, want)
	}
	if st.App != strings.Repeat("*", len(statusValue)) {
		t.Fatalf("stored app = %q, want the redaction", st.App)
	}
	if st.State != "error" {
		t.Fatalf("state = %q, want error", st.State)
	}

	// The rewrite reaches the clients as a fresh status broadcast and the read
	// surface as the stored status.
	frame := waitStatusFrame(t, sub)
	if frame.St == nil || frame.St.Message != want {
		t.Fatalf("broadcast status = %+v, want the redaction", frame.St)
	}
	read := paneRequest(t, cli, msgs, wire.Msg{Type: "req", Action: "read", ID: 2})
	if read.St == nil || read.St.Message != want {
		t.Fatalf("read status = %+v, want the redaction", read.St)
	}
	if strings.Contains(string(mustJSON(t, read)), statusValue) {
		t.Fatal("the read reply holds the value")
	}
}

// TestStatusSetIsAtomicWithTheStatusStore is the race the serial rescrub did
// not close: a report scrubs on the old value list, the set lands and rescrubs
// the status stored before the report, and the report then stores its
// unscubbed text with no later set to revisit it. The store and the set
// serialize on the pane's lock, so the set either lands before the report's
// scrub, which then sees the value, or after the report's store, which the
// set's rescrub then rewrites.
//
// The report is long and the list is at its cap, so the scrub is slow enough to
// be overtaken; the set runs from another goroutine a few microseconds in, the
// window the review reproduced.
func TestStatusSetIsAtomicWithTheStatusStore(t *testing.T) {
	// Not parallel: the race needs the report and the set on two CPUs, and
	// this test is the one that sets the floor. A sequential test runs alone,
	// so the change cannot disturb another test.
	oldGOMAXPROCS := runtime.GOMAXPROCS(2)
	defer runtime.GOMAXPROCS(oldGOMAXPROCS)

	const value = "poc-race-1234"
	// The list at its cap. The scan runs per value per position, so a wide
	// list is what makes the report's scrub long enough to be overtaken. The
	// value joins the list only when the set lands.
	values := make([]string, 0, MaxSecrets)
	for i := 0; i < MaxSecrets-1; i++ {
		values = append(values, fmt.Sprintf("poc-filler-%03d", i))
	}
	withValue := append(append([]string(nil), values...), value)
	// 7000 bytes, the value last, so a scrub that finished before the set
	// misses it.
	raw := strings.Repeat("x", 7000) + value
	want := strings.Repeat("x", 7000) + strings.Repeat("*", len(value))

	for i := 0; i < 30; i++ {
		p := &paneProc{subs: map[*subConn]bool{}, st: wire.Status{State: "idle"}}
		p.setSecrets(values)
		done := make(chan struct{})
		go func() {
			defer close(done)
			p.setStatus(wire.Status{State: "error", Message: raw})
		}()
		time.Sleep(time.Duration(1+i%20) * time.Microsecond)
		p.setSecrets(withValue)
		<-done

		p.mu.Lock()
		st := p.st
		p.mu.Unlock()
		if strings.Contains(st.Message, value) {
			t.Fatalf("iteration %d: stored the value raw", i)
		}
		if st.Message != want {
			t.Fatalf("iteration %d: stored message of %d bytes, want %d with the redaction",
				i, len(st.Message), len(want))
		}
	}
}

// TestStatusWithoutValueIsUnchanged proves a status with no value in it is
// stored as it arrived, and a clear still clears. Nothing about the value list
// changes the ordinary report.
func TestStatusWithoutValueIsUnchanged(t *testing.T) {
	t.Parallel()
	p, _, _ := statusPane(t)
	feedStatus(t, p, wireStatus("working", "pi", "needs review"), 1)
	p.mu.Lock()
	st := p.st
	p.mu.Unlock()
	if st.State != "working" || st.Message != "needs review" || st.App != "pi" {
		t.Fatalf("status = %+v, want it unchanged", st)
	}
	feedStatus(t, p, "\x1b]7501;state=clear\x07", 1)
	p.mu.Lock()
	st = p.st
	p.mu.Unlock()
	if st.State != "unknown" || st.Message != "" {
		t.Fatalf("clear = %+v, want unknown and empty", st)
	}
}

// TestStatusScrubbedWithTheSwitchOff proves the toggle does not reach the
// metadata path. Off is a human's window into the pane's output bytes; the
// status report leaves for the box, a desktop notification and the state file,
// and none of those is a surface the switch was built for.
func TestStatusScrubbedWithTheSwitchOff(t *testing.T) {
	t.Parallel()
	p, _, _ := statusPane(t)
	if p.scrub.toggle() {
		t.Fatal("the switch was already off")
	}
	feedStatus(t, p, wireStatus("error", "tool", statusValue), 1)
	p.mu.Lock()
	st := p.st
	p.mu.Unlock()
	if strings.Contains(st.Message, statusValue) {
		t.Fatalf("the switch off published the value: %q", st.Message)
	}
}

// waitStatusFrame takes the next status frame from a subscriber.
func waitStatusFrame(t *testing.T, sub chan wire.Msg) wire.Msg {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case m, ok := <-sub:
			if !ok {
				t.Fatal("the subscriber closed before the status frame")
			}
			if m.Type == "status" {
				return m
			}
		case <-deadline:
			t.Fatal("no status frame reached the subscriber")
		}
	}
}

// mustJSON renders a frame for a leak check.
func mustJSON(t *testing.T, m wire.Msg) []byte {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
