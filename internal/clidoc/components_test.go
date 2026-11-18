package clidoc

import (
	"reflect"
	"testing"

	"dm/internal/panel"
	"dm/internal/ui"
)

// TestComponentPageNamesEveryField holds the component tables to the value
// types: every exported field and method has a row, and every row names one
// that exists. A prop that lands without a row fails here, and a row that
// outlives its field fails too.
func TestComponentPageNamesEveryField(t *testing.T) {
	for _, c := range components {
		c := c
		switch c.id {
		case "input":
			t.Run(c.name, func(t *testing.T) {
				checkFields(t, "props", c.props, reflect.TypeOf(ui.InputProps{}))
				checkFields(t, "state", c.state, reflect.TypeOf(ui.Input{}))
				checkMethods(t, c.keys, reflect.TypeOf(&ui.Input{}), "")
			})
		case "search":
			t.Run(c.name, func(t *testing.T) {
				if len(c.props) != 0 {
					t.Errorf("search has props; a molecule owns no draw")
				}
				checkFields(t, "state", c.state, reflect.TypeOf(ui.Search{}))
				checkMethods(t, c.keys, reflect.TypeOf(&ui.Search{}), "")
			})
		case "box":
			t.Run(c.name, func(t *testing.T) {
				checkFields(t, "props", c.props, reflect.TypeOf(panel.FloatingPane{}))
				if len(c.state) != 0 {
					t.Errorf("box has state rows; a float owns none")
				}
				if len(c.keys) != 0 {
					t.Errorf("box has key rows; a float never reads a key")
				}
				checkMethods(t, c.keys, reflect.TypeOf(&panel.FloatingPane{}), c.draw)
			})
		case "messagelist":
			t.Run(c.name, func(t *testing.T) {
				checkFields(t, "props", c.props, reflect.TypeOf(ui.MessageListProps{}))
				checkFields(t, "state", c.state, reflect.TypeOf(ui.MessageList{}))
				checkMethods(t, c.keys, reflect.TypeOf(&ui.MessageList{}), c.draw)
			})
		}
	}
}

// checkFields requires one row per exported field of typ, and no row without a
// field.
func checkFields(t *testing.T, table string, rows []componentField, typ reflect.Type) {
	t.Helper()
	have := make(map[string]bool, len(rows))
	for _, r := range rows {
		have[r.name] = true
	}
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if !f.IsExported() {
			continue
		}
		if !have[f.Name] {
			t.Errorf("%s has no row for field %s", table, f.Name)
		}
		have[f.Name] = false
	}
	for name, live := range have {
		if live {
			t.Errorf("%s has a row for %s, which is not a field", table, name)
		}
	}
}

// checkMethods requires one row per exported method of typ, and no row without
// a method. draw names the method that is the component's Draw, which is not a
// key and therefore has no row.
func checkMethods(t *testing.T, rows []componentKey, typ reflect.Type, draw string) {
	t.Helper()
	have := make(map[string]bool, len(rows))
	for _, r := range rows {
		have[r.method] = true
	}
	for i := 0; i < typ.NumMethod(); i++ {
		name := typ.Method(i).Name
		if name == draw {
			continue
		}
		if !have[name] {
			t.Errorf("keys has no row for method %s", name)
		}
		have[name] = false
	}
	for name, live := range have {
		if live {
			t.Errorf("keys has a row for %s, which is not a method", name)
		}
	}
}
