package cli

// The msg show verb: the deterministic pick of one entry and the text it
// prints.

import (
	"testing"

	"dm/internal/box"
)

func showFlags(args ...string) *cmdFlags {
	c := newFlags("msg")
	c.fs.StringVar(&c.key, "key", "", "box key")
	c.fs.IntVar(&c.seq, "seq", -1, "the entry's seq")
	c.fs.StringVar(&c.from, "from", "", "the posting pane")
	_ = c.fs.Parse(args)
	return c
}

func TestShowFlagContract(t *testing.T) {
	c := showFlags("--key", "lane/a", "--seq", "3", "--from", "w1p1")
	if err := boxShowVerb(c); err == nil {
		t.Fatal("naming both a seq and a pane was accepted")
	}
	c = showFlags("--seq", "3")
	if err := boxShowVerb(c); err == nil {
		t.Fatal("a show without a key was accepted")
	}
}

// The pick: the seq's row, else the newest row the pane posted, else the
// topic's newest row.
func TestShowPickEntry(t *testing.T) {
	entries := []box.Entry{
		{Seq: 3, Key: "lane/a", State: box.StateDone, From: "w1p1", Msg: "newest", Body: "long\nbody"},
		{Seq: 2, Key: "lane/a", State: box.StateWorking, From: "w2p1", Msg: "from other pane"},
		{Seq: 1, Key: "lane/a", State: box.StateNote, From: "w1p1", Msg: "oldest from w1p1"},
	}
	e, ok := pickShowEntry(entries, -1, "")
	if !ok || e.Seq != 3 {
		t.Fatalf("default pick = %+v ok %v, want seq 3", e, ok)
	}
	e, ok = pickShowEntry(entries, 1, "")
	if !ok || e.Seq != 1 {
		t.Fatalf("seq pick = %+v ok %v, want seq 1", e, ok)
	}
	e, ok = pickShowEntry(entries, 9, "")
	if ok {
		t.Fatalf("missing seq pick = %+v, want none", e)
	}
	e, ok = pickShowEntry(entries, -1, "w1p1")
	if !ok || e.Seq != 3 {
		t.Fatalf("from pick = %+v ok %v, want the newest w1p1 row", e, ok)
	}
	e, ok = pickShowEntry(entries, -1, "w9p9")
	if ok {
		t.Fatalf("unknown pane pick = %+v, want none", e)
	}
}

// The text a show prints: the body when the entry carries one, the summary
// line when it does not.
func TestShowText(t *testing.T) {
	withBody := box.Entry{Seq: 1, Key: "k", State: box.StateDone, Msg: "summary", Body: "line one\nline two"}
	if text, want := showText(withBody), "line one\nline two"; text != want {
		t.Fatalf("show text = %q, want the body", text)
	}
	without := box.Entry{Seq: 2, Key: "k", State: box.StateDone, Msg: "summary line"}
	if text, want := showText(without), "summary line"; text != want {
		t.Fatalf("show text = %q, want the summary", text)
	}
}
