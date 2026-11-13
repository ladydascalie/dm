package client

import (
	"testing"

	"dm/internal/rail"
	"dm/internal/wire"
)

// windowMoveTUI is a client whose snapshot holds one workspace with three
// windows, in slice order, each with one live pane.
func windowMoveTUI(t *testing.T) (*tui, chan wire.Msg) {
	t.Helper()
	tr, got := clientTUI(t, nil)
	tr.snap.All = []wire.AllPane{
		{ID: "a", Workspace: "s1", Window: "w1"},
		{ID: "b", Workspace: "s1", Window: "w2"},
		{ID: "c", Workspace: "s1", Window: "w3"},
	}
	return tr, got
}

// A middle window's menu offers both moves, an edge window's menu offers the
// one inward move only, and running one sends move-window with its direction.
func TestWindowMenuMoveItems(t *testing.T) {
	t.Parallel()
	tr, got := windowMoveTUI(t)

	// The middle window offers both directions.
	tr.openRailMenu(rail.RailItem{Kind: rail.Window, Workspace: "s1", Window: "w2", WindowName: "two"}, 10, 10)
	labels := map[string]bool{}
	for _, it := range tr.menu.items {
		labels[it.label] = true
		if it.label == "Move window up" {
			tr.closeMenu()
			tr.openRailMenu(rail.RailItem{Kind: rail.Window, Workspace: "s1", Window: "w2", WindowName: "two"}, 10, 10)
			for _, m := range tr.menu.items {
				if m.label == "Move window up" {
					m.run()
				}
			}
			msg := expectRequest(t, got)
			if msg.Action != "move-window" || msg.Target != "w2" || msg.Dir != "up" {
				t.Fatalf("up request = %+v, want move-window w2 up", msg)
			}
		}
		if it.label == "Move window down" {
			tr.closeMenu()
			tr.openRailMenu(rail.RailItem{Kind: rail.Window, Workspace: "s1", Window: "w2", WindowName: "two"}, 10, 10)
			for _, m := range tr.menu.items {
				if m.label == "Move window down" {
					m.run()
				}
			}
			msg := expectRequest(t, got)
			if msg.Action != "move-window" || msg.Target != "w2" || msg.Dir != "down" {
				t.Fatalf("down request = %+v, want move-window w2 down", msg)
			}
		}
	}
	if !labels["Move window up"] || !labels["Move window down"] {
		t.Fatalf("middle window menu labels = %v", labels)
	}

	// The first window has no move up; the last has no move down.
	tr.closeMenu()
	tr.openRailMenu(rail.RailItem{Kind: rail.Window, Workspace: "s1", Window: "w1", WindowName: "one"}, 10, 10)
	for _, it := range tr.menu.items {
		if it.label == "Move window up" {
			t.Fatal("the first window's menu offers move up")
		}
		if it.label == "Move window down" {
			it.run()
			msg := expectRequest(t, got)
			if msg.Dir != "down" || msg.Target != "w1" {
				t.Fatalf("first window's down = %+v, want w1 down", msg)
			}
		}
	}
	tr.closeMenu()
	tr.openRailMenu(rail.RailItem{Kind: rail.Window, Workspace: "s1", Window: "w3", WindowName: "three"}, 10, 10)
	for _, it := range tr.menu.items {
		if it.label == "Move window down" {
			t.Fatal("the last window's menu offers move down")
		}
		if it.label == "Move window up" {
			it.run()
			msg := expectRequest(t, got)
			if msg.Dir != "up" || msg.Target != "w3" {
				t.Fatalf("last window's up = %+v, want w3 up", msg)
			}
		}
	}
}

// A window the All list carries in another workspace ranks in that workspace:
// moves reflect its own block, not the active one.
func TestWindowMenuMoveScopesToWorkspace(t *testing.T) {
	t.Parallel()
	tr, _ := windowMoveTUI(t)
	tr.snap.All = append(tr.snap.All, wire.AllPane{ID: "d", Workspace: "s2", Window: "w9"})
	tr.openRailMenu(rail.RailItem{Kind: rail.Window, Workspace: "s2", Window: "w9", WindowName: "solo"}, 10, 10)
	for _, it := range tr.menu.items {
		if it.label == "Move window up" || it.label == "Move window down" {
			t.Fatalf("a solo window's menu offers %q", it.label)
		}
	}
}
