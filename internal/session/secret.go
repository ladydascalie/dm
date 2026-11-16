package session

import (
	"errors"
	"fmt"
	"log"
	"strings"
)

// Secret is one registered secret in the session's registry. It names how a
// value is fetched and where it rides, never the value itself: a state file
// carries no secret material, and no migration may introduce one.
type Secret struct {
	// Name is the registry key the user picks, for example gh-cli.
	Name string `json:"name"`
	// Tool is the program that prints the value on stdout: op reads an
	// op:// URI, pass reads a store path.
	Tool string `json:"tool"`
	// Reference is what the tool resolves: an op:// URI for op, a store
	// path for pass.
	Reference string `json:"reference"`
	// Upstream pins the URL the proxy forwards a request carrying this
	// secret to. Empty means the secret does not ride the proxy.
	Upstream string `json:"upstream,omitempty"`
	// Header is the header template the proxy fills with the value, with
	// {} as the placeholder, for example "Authorization: Bearer {}".
	// Empty means the secret does not ride the proxy.
	Header string `json:"header,omitempty"`
}

// Valid tools. A tool outside this set cannot be fetched, so load refuses it.
const (
	ToolOp   = "op"
	ToolPass = "pass"
)

// headerSlot is the placeholder a header template must carry for the value.
const headerSlot = "{}"

// Validate reports whether the secret satisfies the registry contract. It
// checks the shape of every field; it never sees a value.
func (s *Secret) Validate() error {
	if strings.TrimSpace(s.Name) == "" {
		return errors.New("secret: empty name")
	}
	switch s.Tool {
	case ToolOp:
		if !strings.HasPrefix(s.Reference, "op://") {
			return fmt.Errorf("secret %s: op reference %q is not an op:// URI", s.Name, s.Reference)
		}
	case ToolPass:
		if s.Reference == "" {
			return fmt.Errorf("secret %s: empty pass reference", s.Name)
		}
		if strings.HasPrefix(s.Reference, "/") {
			return fmt.Errorf("secret %s: pass reference %q is not a store path", s.Name, s.Reference)
		}
	default:
		return fmt.Errorf("secret %s: unknown tool %q", s.Name, s.Tool)
	}
	if s.Header != "" && !strings.Contains(s.Header, headerSlot) {
		return fmt.Errorf("secret %s: header template %q has no %s slot", s.Name, s.Header, headerSlot)
	}
	return nil
}

// Secret returns the registered secret named name, or nil.
func (st *State) Secret(name string) *Secret {
	for _, s := range st.Secrets {
		if s.Name == name {
			return s
		}
	}
	return nil
}

// dropBadSecrets removes entries that fail the registry contract or repeat a
// name already kept, and reports how many went, so LoadState can name them in
// one log line. A hand-edited or truncated state file costs its bad entries,
// not the whole registry.
func (st *State) dropBadSecrets() int {
	if len(st.Secrets) == 0 {
		return 0
	}
	kept := st.Secrets[:0]
	seen := map[string]bool{}
	for _, s := range st.Secrets {
		if s.Validate() != nil || seen[s.Name] {
			continue
		}
		seen[s.Name] = true
		kept = append(kept, s)
	}
	bad := len(st.Secrets) - len(kept)
	if bad > 0 {
		for i := len(kept); i < len(st.Secrets); i++ {
			st.Secrets[i] = nil
		}
	}
	st.Secrets = kept
	return bad
}

// logDroppedSecrets reports one load-time cleanup line for n dropped entries.
func logDroppedSecrets(n int) {
	if n > 0 {
		log.Printf("secrets: dropped %d invalid entries on load", n)
	}
}
