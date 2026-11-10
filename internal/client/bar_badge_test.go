package client

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"dm/internal/box"
	"dm/internal/panel"
	"dm/internal/rail"
	"dm/internal/segment"
	"dm/internal/testutil"
	"dm/internal/theme"
	"dm/internal/wire"
)

// badgeEntry builds one notify entry for the badge tests.
func badgeEntry(seq int, state box.State) box.Entry {
	return box.Entry{Seq: seq, Key: box.NotifyPrefix + "w1p1", State: state, Msg: "msg"}
}

// An unread count renders right-aligned on the bar: the badge is the last
// pinned span, after every existing segment, in the state colour of the
// newest unread entry.
func TestBarBadgeRendersRightAligned(t *testing.T) {
	th := theme.DefaultTheme()
	snap := &wire.Snapshot{Cols: 60, Rows: 24}
	set := theme.GlyphNerd
	glyph, colour := rail.RailGlyph(th, "blocked", set)
	badge := barBadge{n: 3, glyph: glyph, colour: colour}

	segs := barSegments(th, snap, false, "", set, badge)
	last := segs[len(segs)-1]
	if want := " " + glyph + " 3"; last.Text != want {
		t.Fatalf("last span = %q, want the badge %q", last.Text, want)
	}
	if !last.Pin {
		t.Fatal("the badge is not pinned to the last column")
	}
	if last.FG != colour {
		t.Fatalf("badge colour = %v, want the state colour %v", last.FG, colour)
	}
	if n := segment.SegCells(segs); n > snap.Cols {
		t.Fatalf("bar paints %d cells, want at most %d", n, snap.Cols)
	}

	// The badge rides the end of the pinned block, after the info segment.
	info := "SCROLL w1p1 +1/9"
	segs = barSegments(th, snap, true, info, set, badge)
	texts := testutil.BarTexts(segs)
	if len(texts) < 3 || texts[len(texts)-1] != " "+glyph+" 3" {
		t.Fatalf("bar spans = %q, want the badge last", texts)
	}

	// The drawn frame carries the badge.
	var b bytes.Buffer
	drawBar(&b, th, snap, true, info, set, badge)
	if !strings.Contains(b.String(), glyph+" 3") {
		t.Fatalf("frame misses the badge: %q", b.String())
	}
}

// Zero unread renders nothing: the bar is byte for byte the bar without a
// badge.
func TestBarBadgeHiddenAtZero(t *testing.T) {
	th := theme.DefaultTheme()
	snap := &wire.Snapshot{Cols: 60, Rows: 24}
	set := theme.GlyphNerd
	info := "SCROLL w1p1 +1/9"

	plain := barSegments(th, snap, true, info, set)
	withBadge := barSegments(th, snap, true, info, set, barBadge{})
	if len(plain) != len(withBadge) {
		t.Fatalf("span count = %d, want %d", len(withBadge), len(plain))
	}
	for i := range plain {
		if plain[i].Text != withBadge[i].Text || plain[i].Pin != withBadge[i].Pin {
			t.Fatalf("span %d = %q, want %q", i, withBadge[i].Text, plain[i].Text)
		}
	}
	var b bytes.Buffer
	drawBar(&b, th, snap, true, info, set, barBadge{})
	var bare bytes.Buffer
	drawBar(&bare, th, snap, true, info, set)
	if b.String() != bare.String() {
		t.Fatalf("a zero badge changed the frame:\n%q\n%q", b.String(), bare.String())
	}
}

// A tui fed by the notify replies computes its badge from the entries past
// the read mark, so the bar and the centre's unread marks agree.
func TestBarBadgeAgreesWithCentre(t *testing.T) {
	tui := &tui{}
	tui.notifyRecent = []box.Entry{
		badgeEntry(9, box.StateBlocked),
		badgeEntry(8, box.StateDone),
		badgeEntry(7, box.StateNote),
	}
	c := &notifyCentre{entries: tui.notifyRecent}

	if n := tui.barBadgeOf(theme.DefaultTheme(), theme.GlyphNerd).n; n != 3 {
		t.Fatalf("badge = %d, want 3 unread", n)
	}
	c.read = tui.notifyRead
	if u := c.unread(); u != 3 {
		t.Fatalf("centre unread = %d, want 3", u)
	}

	// Opening the newest entry raises the read mark: both drop together.
	tui.notifyRead = 9
	c.read = 9
	if n := tui.barBadgeOf(theme.DefaultTheme(), theme.GlyphNerd).n; n != 0 {
		t.Fatalf("badge = %d, want 0 after reading up to 9", n)
	}
	if u := c.unread(); u != 0 {
		t.Fatalf("centre unread = %d, want 0", u)
	}

	// A newer entry lands: both rise together.
	tui.notifyRecentMerge([]box.Entry{badgeEntry(12, box.StateError)})
	c.entries = tui.notifyRecent
	c.read = tui.notifyRead
	if n := tui.barBadgeOf(theme.DefaultTheme(), theme.GlyphNerd).n; n != 1 {
		t.Fatalf("badge = %d, want 1", n)
	}
	if u := c.unread(); u != 1 {
		t.Fatalf("centre unread = %d, want 1", u)
	}
}

// Clearing entries moves the badge: a cleared key and a cleared topic leave
// the cache, so the badge follows the centre's list.
func TestBarBadgeClearMoves(t *testing.T) {
	tui := &tui{}
	tui.notifyRecent = []box.Entry{
		{Seq: 9, Key: "notify/w1p1", State: box.StateBlocked, Msg: "m"},
		{Seq: 8, Key: "notify/w1p2", State: box.StateError, Msg: "m"},
		{Seq: 7, Key: "notify/w1p1", State: box.StateDone, Msg: "m"},
	}

	tui.notifyRecentDropKey("notify/w1p1")
	if n := tui.notifyUnread(); n != 1 {
		t.Fatalf("badge = %d, want 1 after clearing notify/w1p1", n)
	}

	tui.notifClearAll()
	if n := tui.notifyUnread(); n != 0 {
		t.Fatalf("badge = %d, want 0 after clearing the topic", n)
	}
	if len(tui.notifyRecent) != 0 {
		t.Fatalf("cache = %v, want empty", tui.notifyRecent)
	}
}

// The catch-up reply feeds the badge: the merged entries stay newest first
// and only the unseen seqs join.
func TestBarBadgeFeedFromCatchUp(t *testing.T) {
	tui := &tui{}
	tui.notifyRecent = []box.Entry{badgeEntry(7, box.StateNote)}
	rep := box.ListReply{Entries: []box.Entry{
		badgeEntry(9, box.StateBlocked),
		badgeEntry(8, box.StateDone),
	}}
	body, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	tui.takeNotifyList(wire.Msg{Type: "resp", OK: true, Action: "msg-list", Res: body})

	if got := tui.notifyUnread(); got != 3 {
		t.Fatalf("badge = %d, want 3: every cached entry is still unread", got)
	}
	if len(tui.notifyRecent) != 3 || tui.notifyRecent[0].Seq != 9 || tui.notifyRecent[2].Seq != 7 {
		t.Fatalf("cache = %v, want newest first 9, 8, 7", tui.notifyRecent)
	}

	// The same reply again merges nothing.
	tui.takeNotifyList(wire.Msg{Type: "resp", OK: true, Action: "msg-list", Res: body})
	if n := len(tui.notifyRecent); n != 3 {
		t.Fatalf("cache = %d entries, want 3 after a repeat", n)
	}
}

// The centre's reply is the truth while the centre is open: it replaces the
// cache, so the badge matches the rows the float shows.
func TestBarBadgeFeedFromCentre(t *testing.T) {
	tui := &tui{snap: &wire.Snapshot{Cols: 60, Rows: 24}, ncentre: &notifyCentre{
		Float: &panel.FloatingPane{ID: "notif", Active: true},
	}}
	tui.notifyRecent = []box.Entry{badgeEntry(99, box.StateNote)}
	rep := box.ListReply{Entries: []box.Entry{badgeEntry(11, box.StateError)}}
	body, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	tui.takeNotifyCentreList(wire.Msg{Type: "resp", OK: true, Action: "msg-list", Res: body})

	if len(tui.notifyRecent) != 1 || tui.notifyRecent[0].Seq != 11 {
		t.Fatalf("cache = %v, want the centre's list alone", tui.notifyRecent)
	}
	if n := tui.notifyUnread(); n != 1 {
		t.Fatalf("badge = %d, want 1", n)
	}
}
