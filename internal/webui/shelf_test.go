package webui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"dm/internal/wire"
)

// TestFleetMarksTheShelfRow proves the fleet carries the shelf: the row keeps
// its id, name, status and folder, it is marked, and it names no window because
// a shelved pane is in none.
func TestFleetMarksTheShelfRow(t *testing.T) {
	session := "main"
	dir := root(t, session)
	snap := wire.Snapshot{
		Panes: []wire.PaneInfo{{ID: "w1p1"}, {ID: "w1p2", Shelf: true}},
		All: []wire.AllPane{
			{ID: "w1p1", Name: "pi", Workspace: "s1", WorkspaceName: "dm", Window: "w1", WindowName: "main", Focus: true},
			{ID: "w1p2", Name: "set aside", Status: "blocked", Message: "may I?", Agent: true,
				Cwd: "/tmp/aside", Branch: "poc/web", Dirty: true, Shelf: true},
		},
	}
	fakeSession(t, dir, session, snap, nil)
	srv := httptest.NewServer(handler(Config{Session: session, Root: dir, Token: "t"}))
	defer srv.Close()

	res := get(t, srv.URL, "/api/fleet?t=t")
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	var got fleetReply
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got.Panes) != 2 {
		t.Fatalf("%d rows, want 2", len(got.Panes))
	}
	if live := got.Panes[0]; live.ID != "w1p1" || live.Shelf {
		t.Fatalf("live row %+v, want no shelf mark", live)
	}
	row := got.Panes[1]
	if row.ID != "w1p2" || !row.Shelf {
		t.Fatalf("shelf row %+v, want the shelf mark", row)
	}
	if row.Name != "set aside" || row.Status != "blocked" || row.Message != "may I?" || !row.Agent {
		t.Fatalf("shelf row lost its facts: %+v", row)
	}
	if row.Cwd != "/tmp/aside" || row.Branch != "poc/web" || !row.Dirty {
		t.Fatalf("shelf row lost its folder: %+v", row)
	}
	if row.Workspace != "" || row.Window != "" || row.Active {
		t.Fatalf("shelf row invented a place: %+v", row)
	}
}

// TestPopPaneCatalogueRow pins the pop contract the page builds its menu from:
// one row for pop-pane, it takes a pane target, and it names no other field.
func TestPopPaneCatalogueRow(t *testing.T) {
	cmd, ok := commandByID("pop-pane")
	if !ok {
		t.Fatal("the catalogue has no pop-pane row")
	}
	if cmd.Target != targetPane {
		t.Fatalf("pop-pane target kind = %q, want %q", cmd.Target, targetPane)
	}
	if !reflect.DeepEqual(cmd.Fields, []string{"target"}) {
		t.Fatalf("pop-pane fields = %v, want [target]", cmd.Fields)
	}
	if cmd.Group != groupPanes {
		t.Fatalf("pop-pane group = %q, want %q", cmd.Group, groupPanes)
	}
}

// TestPopPaneRefusedReadOnly proves a read only page cannot pop: the action is a
// write, so it answers 403 and never reaches the session.
func TestPopPaneRefusedReadOnly(t *testing.T) {
	dir := root(t, "main")
	log := recordSession(t, dir, "main", `{"pane":"w1p1"}`, "")
	srv := httptest.NewServer(handler(Config{Session: "main", Root: dir, Token: "t", ReadOnly: true}))
	defer srv.Close()

	res := post(t, srv.URL+"/api/action?t=t", map[string]string{"id": "pop-pane", "target": "w1p1"})
	if got := body(t, res); res.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d: %s", res.StatusCode, got)
	}
	if n := len(log.all()); n != 0 {
		t.Fatalf("%d requests reached the session, want none", n)
	}
}

// TestPageShelfStartsFoldedWithItsCount pins the shelf the page paints: its
// heading carries the count of panes it sets aside, the fold starts closed, and
// the rows hang off that fold alone.
func TestPageShelfStartsFoldedWithItsCount(t *testing.T) {
	src := appScript(t)
	if !strings.Contains(src, "let shelfOpen = false;") {
		t.Fatal("the shelf fold does not start closed")
	}
	count := funcSource(t, src, "shelfCount")
	for _, want := range []string{`" shelved pane"`, `" shelved panes"`} {
		if !strings.Contains(count, want) {
			t.Fatalf("shelfCount carries no %s: %s", want, count)
		}
	}
	sec := funcSource(t, src, "shelfSection")
	if !strings.Contains(sec, "shelfCount(panes.length)") {
		t.Fatalf("the shelf heading carries no count: %s", sec)
	}
	if !strings.Contains(sec, `aria-expanded", shelfOpen ? "true" : "false"`) {
		t.Fatalf("the shelf fold does not read shelfOpen: %s", sec)
	}
	if !strings.Contains(sec, "if (shelfOpen) for (const p of panes)") {
		t.Fatalf("the shelf rows are not behind the fold: %s", sec)
	}
}

// TestPageShelfSectionSitsUnderTheFleet pins the section's place and its marks:
// the shelf rows leave the project groups, the section follows them, and a
// shelved row says so rather than passing for a live pane.
func TestPageShelfSectionSitsUnderTheFleet(t *testing.T) {
	src := appScript(t)
	list := funcSource(t, src, "renderPanes")
	if !strings.Contains(list, "filter((p) => !p.shelf)") || !strings.Contains(list, "filter((p) => p.shelf)") {
		t.Fatalf("renderPanes does not split the shelf from the fleet: %s", list)
	}
	at, shelf := strings.Index(list, "projectSection(g)"), strings.Index(list, "shelfSection(shelved)")
	if at < 0 || shelf < 0 || shelf < at {
		t.Fatalf("the shelf is not painted under the fleet: %s", list)
	}
	row := funcSource(t, src, "paneRow")
	if !strings.Contains(row, `p.shelf ? " shelved" : ""`) {
		t.Fatalf("a shelved row carries no mark: %s", row)
	}
}

// TestPagePopTapOnlyOnAShelvedPane proves the pop control follows the fleet: it
// is offered while the open pane is set aside and gone once it is back.
func TestPagePopTapOnlyOnAShelvedPane(t *testing.T) {
	src := appScript(t)
	if !strings.Contains(src, "{ label: \"pop pane\", body: () => ({ id: \"pop-pane\", target: pane.id }), shelved: true }") {
		t.Fatal("the pane's actions carry no pop control")
	}
	acts := funcSource(t, src, "renderPaneActions")
	if !strings.Contains(acts, "if (act.shelved && !own.shelf) continue;") {
		t.Fatalf("the pop control is not gated on the shelf: %s", acts)
	}
}
