// Package configdoc holds the config surface dm documents: the config.kdl
// nodes, the default keymaps, the environment variables, the files and the
// theme-file keys. It is plain data. The docs render from these tables, and the
// tests prove each table against the parser that reads it.
package configdoc

// ConfigNode is one top-level config.kdl node: its name, the value shape it
// accepts, its built-in default and one sentence on what it changes.
type ConfigNode struct {
	Name    string
	Value   string
	Default string
	Desc    string
}

// EnvVar is one environment variable dm reads. TestOnly marks a variable that
// only the test harness reads.
type EnvVar struct {
	Name     string
	Desc     string
	TestOnly bool
}

// File is one file dm reads or writes. Write marks a file dm creates or
// rewrites.
type File struct {
	Path  string
	Desc  string
	Write bool
}

// ThemeKey is one theme-file key. Block is the top-level block, or the state
// name prefixed with "states." for a key inside a state block. Default is the
// built-in value as the file spells it.
type ThemeKey struct {
	Block   string
	Name    string
	Default string
	Desc    string
}

// Mode is one keymap mode: its name, one sentence on when it is active, and
// every action the built-in config binds in it.
type Mode struct {
	Name    string
	Desc    string
	Actions []Action
}

// Action is one keymap binding: the action name, the built-in key and one
// sentence on what the action does.
type Action struct {
	Name string
	Key  string
	Desc string
}
