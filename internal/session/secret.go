package session

import (
	"errors"
	"fmt"
	"log"
	"slices"
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
	// Env holds extra environment entries the fetch tool needs, as
	// KEY=VALUE. Only the tool configuration names in secretEnvKeys pass:
	// the fetch child runs with them, so anything else would be code
	// execution or a credential at rest. A tool that needs a token reads it
	// from its own environment.
	Env []string `json:"env,omitempty"`
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
	seen := map[string]bool{}
	for i, kv := range s.Env {
		key, _, found := strings.Cut(kv, "=")
		if !found || key == "" {
			// The entry is named by index and length only: with no = the
			// whole entry can be a value.
			return fmt.Errorf("secret %s: env entry %d is not KEY=VALUE: %d bytes", s.Name, i, len(kv))
		}
		if strings.ContainsAny(kv, "\r\n") {
			// The key is not named here either: a line break can put the
			// value in the key position.
			return fmt.Errorf("secret %s: env entry %d carries a line break: %d bytes", s.Name, i, len(kv))
		}
		if !allowedEnvName(key) {
			return fmt.Errorf("secret %s: env entry %d: key %s is not tool configuration; allowed keys: %s; the entry is %d bytes",
				s.Name, i, envKeyLabel(key), strings.Join(secretEnvKeys, ", "), len(kv))
		}
		// The key passed the allowlist, so naming it here is safe.
		if seen[key] {
			return fmt.Errorf("secret %s: env key %s appears twice", s.Name, key)
		}
		seen[key] = true
	}
	return nil
}

// secretEnvKeys is the whole of what a secret record's env may carry: the
// configuration a fetch tool reads to find its own store. It is a positive
// allowlist on purpose. A denylist of credential-shaped words cannot name the
// routes that matter: LD_PRELOAD and LD_LIBRARY_PATH run code in the fetch
// child, PATH picks the shell the tool's own shebang resolves, and a
// credential-shaped name such as DB_PASS or OP_SESSION_x would put a value in
// the state file. Every entry below is a name a tool reads to locate itself,
// and no entry is a value. Keep the list small and keep the match exact.
var secretEnvKeys = []string{
	"GNUPGHOME",                  // pass: the GnuPG home that holds the store key
	"HOME",                       // both tools: the user's dotfiles, and the default store
	"LANG",                       // both tools: message locale
	"LC_ALL",                     // both tools: message locale
	"OP_ACCOUNT",                 // op: the account a reference is read from
	"OP_CONFIG_DIR",              // op: the config directory that holds the account
	"PASSWORD_STORE_DIR",         // pass: the store root
	"PASSWORD_STORE_KEY",         // pass: the key id that signs the store
	"PASSWORD_STORE_SIGNING_KEY", // pass: the key id that verifies the store's signatures
	"XDG_CONFIG_HOME",            // op: the config root, when OP_CONFIG_DIR is unset
	"XDG_DATA_HOME",              // op: the session data root
	"XDG_RUNTIME_DIR",            // op: the session socket directory
}

// allowedEnvName reports whether name is one of the tool configuration names a
// record may carry. The match is exact and case-sensitive: an environment name
// is case-sensitive, so a near miss such as home or HOME_EXTRA is refused
// rather than folded onto an allowed key.
func allowedEnvName(name string) bool {
	return slices.Contains(secretEnvKeys, name)
}

// envKeyLabel renders a refused env key for a message. A key that is not on
// the allowlist can be any text, a pasted value included, so a long one is cut
// and its full length kept.
func envKeyLabel(key string) string {
	const maxKey = 64
	if len(key) > maxKey {
		return fmt.Sprintf("%q (%d bytes)", key[:maxKey], len(key))
	}
	return fmt.Sprintf("%q", key)
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
		// A JSON null in a hand-edited file unmarshals to a nil pointer.
		// It is a bad entry like any other, not a failed load.
		if s == nil || s.Validate() != nil || seen[s.Name] {
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
