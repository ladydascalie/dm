package palette

import "testing"

func cmds() []Command {
	return []Command{
		{Name: "New Pane", Hint: "open a pane", Action: "new-pane"},
		{Name: "Close Pane", Hint: "close the pane", Action: "close-pane"},
		{Name: "Split Right", Hint: "split the pane", Action: "split-right"},
	}
}

func TestNewWithEmptyQueryListsAllInInputOrder(t *testing.T) {
	m := New(cmds())
	got := m.Rows()
	if len(got) != 3 || got[0].Action != "new-pane" || got[2].Action != "split-right" {
		t.Errorf("Rows() = %v, want all three in input order", got)
	}
	if sel, ok := m.Selected(); !ok || sel.Action != "new-pane" {
		t.Errorf("Selected() = %v, %v, want new-pane", sel, ok)
	}
	if m.SelectedIndex() != 0 {
		t.Errorf("SelectedIndex() = %d, want 0", m.SelectedIndex())
	}
}

func TestAppendRuneFiltersAndBackspaceRestores(t *testing.T) {
	m := New(cmds())
	m.AppendRune('s')
	m.AppendRune('p')
	m.AppendRune('l')
	got := m.Rows()
	if len(got) != 1 || got[0].Action != "split-right" {
		t.Errorf("Rows() after \"spl\" = %v, want split-right", got)
	}
	m.Backspace()
	m.Backspace()
	if got := m.Rows(); len(got) != 2 || got[0].Action != "split-right" {
		t.Errorf("Rows() after two backspaces = %v, want two rows led by split-right", got)
	}
	m.Backspace()
	if got := m.Rows(); len(got) != 3 {
		t.Errorf("Rows() after three backspaces = %v, want all three", got)
	}
}

func TestBackspaceOnEmptyQueryDoesNothing(t *testing.T) {
	m := New(cmds())
	m.Backspace()
	if m.Query() != "" {
		t.Errorf("Query() = %q, want empty", m.Query())
	}
	if len(m.Rows()) != 3 {
		t.Errorf("Rows() = %v, want all three", m.Rows())
	}
}

func TestSelectionClampsWhenRowsShrink(t *testing.T) {
	m := New(cmds())
	m.Cycle(2)
	if m.SelectedIndex() != 2 {
		t.Fatalf("SelectedIndex() = %d, want 2", m.SelectedIndex())
	}
	m.AppendRune('s') // Split Right and Close Pane survive
	if got := m.SelectedIndex(); got != 1 {
		t.Errorf("SelectedIndex() = %d, want clamped to 1", got)
	}
	if sel, ok := m.Selected(); !ok || sel.Action != "close-pane" {
		t.Errorf("Selected() = %v, %v, want close-pane", sel, ok)
	}
	m.Backspace() // back to all three, the clamp holds
	if got := m.SelectedIndex(); got != 1 {
		t.Errorf("SelectedIndex() = %d, want 1", got)
	}
}

func TestSelectionOnEmptyRows(t *testing.T) {
	m := New(cmds())
	m.Cycle(2)
	m.AppendRune('z') // matches nothing
	if len(m.Rows()) != 0 {
		t.Fatalf("Rows() after 'z' = %v, want none", m.Rows())
	}
	if _, ok := m.Selected(); ok {
		t.Error("Selected() reported a row with none to pick")
	}
	if m.SelectedIndex() != 0 {
		t.Errorf("SelectedIndex() = %d, want 0", m.SelectedIndex())
	}
	m.Cycle(1)
	m.Cycle(-1)
	if m.SelectedIndex() != 0 {
		t.Errorf("SelectedIndex() after cycle = %d, want 0", m.SelectedIndex())
	}
	m.Backspace() // rows return, selection starts over at the top
	if got := m.SelectedIndex(); got != 0 {
		t.Errorf("SelectedIndex() = %d, want 0", got)
	}
}

func TestCycleWrapsBothWays(t *testing.T) {
	m := New(cmds())
	m.Cycle(1)
	if m.SelectedIndex() != 1 {
		t.Errorf("down: SelectedIndex() = %d, want 1", m.SelectedIndex())
	}
	m.Cycle(-1)
	if m.SelectedIndex() != 0 {
		t.Errorf("up: SelectedIndex() = %d, want 0", m.SelectedIndex())
	}
	m.Cycle(-1)
	if m.SelectedIndex() != 2 {
		t.Errorf("wrap up: SelectedIndex() = %d, want 2", m.SelectedIndex())
	}
	m.Cycle(1)
	if m.SelectedIndex() != 0 {
		t.Errorf("wrap down: SelectedIndex() = %d, want 0", m.SelectedIndex())
	}
	m.Cycle(3)
	if m.SelectedIndex() != 0 {
		t.Errorf("full lap: SelectedIndex() = %d, want 0", m.SelectedIndex())
	}
}

func TestQueryKeepsUnicodeRune(t *testing.T) {
	m := New(cmds())
	m.AppendRune('é')
	if got := m.Query(); got != "é" {
		t.Errorf("Query() = %q, want é", got)
	}
	m.AppendRune('w')
	m.Backspace()
	if got := m.Query(); got != "é" {
		t.Errorf("Query() = %q, want é", got)
	}
}
