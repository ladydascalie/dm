package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"dm/internal/keymap"
)

// configReport is the shape `dm config --json` prints. ok is false when the
// file does not parse, and error carries the whole line, which is the text a
// toast has no room for. note names a case that is not a parse at all, and
// stale carries an earlier failure the file no longer holds.
type configReport struct {
	Path  string `json:"path"`
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
	Note  string `json:"note,omitempty"`
	Stale string `json:"stale,omitempty"`
}

// runConfig reports whether the config file parses and exits non-zero when it
// does not. A client announces a load failure in one toast row that elides the
// text and dies in three seconds, so this verb is where the whole line gets
// read. It writes nothing: a load records its own failure beside the file.
func runConfig(c *cmdFlags) error {
	path, err := keymap.DefaultPath()
	if err != nil {
		return err
	}
	rep := configReport{Path: path, OK: true}
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		rep.Note = "absent, built-in defaults"
	} else if _, err := keymap.LoadPath(path); err != nil {
		rep.OK, rep.Error = false, err.Error()
	} else if data, err := os.ReadFile(path + keymap.ErrorSuffix); err == nil {
		rep.Stale = strings.TrimSpace(string(data))
	}
	if c.jsonOut {
		out, err := json.Marshal(rep)
		if err != nil {
			return fmt.Errorf("config: %w", err)
		}
		fmt.Println(string(out))
	} else if rep.OK {
		if rep.Note != "" {
			fmt.Printf("ok\t%s\t%s\n", rep.Path, rep.Note)
		} else {
			fmt.Printf("ok\t%s\n", rep.Path)
		}
		if rep.Stale != "" {
			fmt.Printf("stale\t%s\n", rep.Stale)
		}
	}
	if !rep.OK {
		return errors.New(rep.Error)
	}
	return nil
}
