package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"dm/internal/pane"
	"dm/internal/paths"
	"dm/internal/proxy"
	"dm/internal/secrets"
	"dm/internal/session"
	"dm/internal/wire"
)

// The secret actions move a fetched value through exactly five places: the
// secrets registry, the proxy's upstream request, the keystroke burst a
// secret type sends, the redaction registries the panes hold and the sibling
// servers a push asks to scrub the value in their own panes. The value never
// reaches argv, the logs, the state file, the box or a reply body: every
// reply names the secret, never the value.

// secretEnsure builds the secret state on first use, under s.mu. A server
// that never touches a secret action allocates nothing.
func (s *server) secretEnsure() {
	if s.secrets == nil {
		s.secrets = secrets.NewRegistry()
	}
	if s.secretActive == nil {
		s.secretActive = map[string]bool{}
	}
	if s.secretPrefixes == nil {
		s.secretPrefixes = map[string]string{}
	}
	if s.secretForeign == nil {
		s.secretForeign = map[string][]string{}
	}
}

// secretTool maps a registered secret to the command that prints its value.
// The reference is an argument, never a value. The child inherits the
// configuration and paths the tools need, and nothing else: the entries the
// secret record carries come on top and a credential name is refused at
// register time, so a token never reaches the state file. op reads its
// service-account token from the operator's own environment, which rides
// process to child and nowhere else.
func secretTool(sec *session.Secret) secrets.Tool {
	switch sec.Tool {
	case session.ToolOp:
		return secrets.Tool{
			Name: "op", Bin: "op", Args: []string{"read", sec.Reference},
			Inherit: append(toolInherit, "OP_SERVICE_ACCOUNT_TOKEN"),
			Env:     sec.Env,
		}
	case session.ToolPass:
		return secrets.Tool{
			Name: "pass", Bin: "pass", Args: []string{"show", sec.Reference},
			Inherit: toolInherit,
			Env:     sec.Env,
		}
	}
	return secrets.Tool{Name: sec.Tool}
}

// toolInherit names the variables a fetch tool child needs from the server's
// environment: account paths, the store location and the GnuPG home. All
// configuration, no credentials.
var toolInherit = []string{
	"HOME", "PATH", "USER", "LOGNAME", "LANG", "TMPDIR",
	"GNUPGHOME", "PASSWORD_STORE_DIR",
	"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME",
	"XDG_RUNTIME_DIR",
}

// fetchSecret runs the secret's tool and returns the value. The stub field
// keeps tests off PATH.
func (s *server) fetchSecret(sec *session.Secret) (string, error) {
	if s.fetch != nil {
		return s.fetch(sec)
	}
	return secrets.Fetch(secretTool(sec))
}

// secretValuesLocked returns the live values in secret-name order, capped at
// the panes' list size. The caller holds s.mu: the active map and the
// registry are shared with every connection goroutine. The order and the cap
// are the survivor policy: the pane keeps the first MaxSecrets values it is
// pushed, so the server decides deterministically which names make the cut
// and can name the ones that did not.
func (s *server) secretValuesLocked() ([]string, []string) {
	s.secretEnsure()
	names := make([]string, 0, len(s.secretActive))
	for name := range s.secretActive {
		names = append(names, name)
	}
	sort.Strings(names)
	values := make([]string, 0, len(names))
	var dropped []string
	for _, name := range names {
		v, ok := s.secrets.Get(name)
		if !ok {
			continue
		}
		if len(values) == pane.MaxSecrets {
			dropped = append(dropped, name)
			continue
		}
		values = append(values, v)
	}
	return values, dropped
}

// pushSecretScrub sends the live values to every adopted pane, so their
// output stage redacts each one, and asks every sibling session's server to
// do the same for its own panes: the endpoint a value rides answers on
// loopback, so a push that stopped at this session's border would leave the
// other sessions' panes unscrubbed. Collecting the set and sending it are one
// step under secretPush: a pane's scrubber replaces its whole list, so two
// pushes that interleave could leave a pane with the stale one, and the adopt
// push below races the same way. The returned map names the panes that
// refused the set, pane ids and sibling sessions alike; an empty map means
// every live pane redacts. An empty push is the point of a revoke: the panes
// must hear the set is gone, not stay with a stale value on their list.
func (s *server) pushSecretScrub() map[string]error {
	s.secretPush.Lock()
	defer s.secretPush.Unlock()
	failed := s.sendSecretScrub()
	for name, err := range s.coverSiblings() {
		failed["session "+name] = err
	}
	return failed
}

// pushLocalScrub sends the merged set to this session's own panes and goes no
// further: the sessions the foreign values came from push those values
// themselves, so a cover never rebroadcasts what it was handed.
func (s *server) pushLocalScrub() map[string]error {
	s.secretPush.Lock()
	defer s.secretPush.Unlock()
	return s.sendSecretScrub()
}

// sendSecretScrub collects the current set and sends it to every adopted
// pane. The caller holds secretPush.
func (s *server) sendSecretScrub() map[string]error {
	s.mu.Lock()
	values := s.scrubValuesLocked()
	panes := make([]*paneConn, 0, len(s.panes))
	for _, pc := range s.panes {
		panes = append(panes, pc)
	}
	s.mu.Unlock()
	body := jsonRaw(map[string]any{"values": values})
	failed := map[string]error{}
	missed := []string{}
	for _, pc := range panes {
		// A poisoned conn refuses every send at once: skip it instead of
		// burning another timeout per push, and report it failed like any
		// other pane that missed the set.
		if pc.isDegraded() {
			failed[pc.id] = errPaneDegraded
			missed = append(missed, pc.id)
			continue
		}
		if err := pc.send(wire.Msg{Action: "set-secrets", Res: body}); err != nil {
			failed[pc.id] = err
			missed = append(missed, pc.id)
		}
	}
	// A pane the push reached for is an explicit later action: re-arm its
	// wedged watcher, so the operator's remedy actually runs.
	for _, id := range missed {
		s.rearmPaneWatcher(id)
	}
	return failed
}

// adoptSecretScrub gives a freshly adopted pane the live set. It runs under
// secretPush after the conn is registered, so a concurrent action's push
// either lands before this one with a set this collection then repeats, or
// waits and pushes a newer one; the pane can never end on a stale list.
func (s *server) adoptSecretScrub(id string) {
	s.secretPush.Lock()
	defer s.secretPush.Unlock()
	s.mu.Lock()
	values := s.scrubValuesLocked()
	pc := s.panes[id]
	s.mu.Unlock()
	if len(values) == 0 || pc == nil {
		return
	}
	// A poisoned conn refuses every send at once; skipping it here keeps
	// the adopt under secretPush as short as every other push.
	if pc.isDegraded() {
		return
	}
	if err := pc.send(wire.Msg{Action: "set-secrets", Res: jsonRaw(map[string]any{"values": values})}); err != nil {
		log.Printf("pane %s: set-secrets: %v", id, err)
	}
}

// scrubValuesLocked is the set the panes must redact: this session's live
// values first, in name order, then the values other sessions' servers asked
// this one to cover, deduplicated against them, capped at the panes' list
// size. The caller holds s.mu.
func (s *server) scrubValuesLocked() []string {
	values, _ := s.secretValuesLocked()
	if len(s.secretForeign) == 0 {
		return values
	}
	seen := make(map[string]bool, len(values))
	for _, v := range values {
		seen[v] = true
	}
	sources := make([]string, 0, len(s.secretForeign))
	for name := range s.secretForeign {
		sources = append(sources, name)
	}
	sort.Strings(sources)
	for _, name := range sources {
		for _, v := range s.secretForeign[name] {
			if seen[v] || len(values) == pane.MaxSecrets {
				continue
			}
			values = append(values, v)
			seen[v] = true
		}
	}
	return values
}

// siblingDialTimeout bounds one dial to a sibling session's server or pane
// socket. A dead socket refuses at once; the timeout only bounds a socket
// that answers slowly.
const siblingDialTimeout = 100 * time.Millisecond

// coverRoundtripTimeout bounds one cover request to a sibling server: the
// write and the wait for its reply.
const coverRoundtripTimeout = 2 * time.Second

// coverSiblings asks every other session's live server to scrub this
// session's values in its own panes. A push from here cannot reach a pane
// another server adopted, and the set must not stop at this session's border:
// the endpoint a value rides answers on loopback, so a pane in any session
// can echo it. The origin session owns its values: it pushes every change and
// the withdrawal, and the sibling folds the set into every push its own
// panes hear, so one writer stays in charge of each pane's list. The caller
// holds secretPush, so the covers leave in the same order as the pushes to
// this session's own panes. A sibling whose server never answers is no
// failure when nothing of it is live; one with live panes and no server is
// named instead of pushed, because a push to an orphaned pane has no writer
// left to keep its list in step.
func (s *server) coverSiblings() map[string]error {
	s.mu.Lock()
	s.secretEnsure()
	values, _ := s.secretValuesLocked()
	var root, own string
	if s.st != nil {
		root, own = s.st.Dir(), s.st.Name
	}
	s.mu.Unlock()
	if root == "" {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(root, "sessions"))
	if err != nil {
		return nil
	}
	failed := map[string]error{}
	for _, e := range entries {
		name := e.Name()
		if name == own || !e.IsDir() || !paths.ValidSessionName(name) {
			continue
		}
		if err := s.coverSession(root, own, name, values); err != nil {
			failed[name] = err
		}
	}
	return failed
}

// coverSession covers one sibling session: through its live server when it
// has one, since that server folds the set into every push its panes hear,
// now and on every later push and adoption.
func (s *server) coverSession(root, own, name string, values []string) error {
	c, err := net.DialTimeout("unix", paths.ServerSockAt(root, name), siblingDialTimeout)
	if err == nil {
		defer func() { _ = c.Close() }()
		return coverRoundtrip(c, own, values)
	}
	live := livePaneSockets(root, name)
	if live == 0 {
		return nil
	}
	if live == 1 {
		return errors.New("no live server; 1 live pane uncovered")
	}
	return fmt.Errorf("no live server; %d live panes uncovered", live)
}

// livePaneSockets counts the pane sockets a session still serves: a socket
// that dials is a live pane, a stale one answers nothing.
func livePaneSockets(root, name string) int {
	panes, err := os.ReadDir(filepath.Join(root, "sessions", name, "panes"))
	if err != nil {
		return 0
	}
	live := 0
	for _, pe := range panes {
		id, ok := strings.CutSuffix(pe.Name(), ".sock")
		if !ok {
			continue
		}
		c, err := net.DialTimeout("unix", paths.PaneSockAt(root, name, id), siblingDialTimeout)
		if err != nil {
			continue
		}
		_ = c.Close()
		live++
	}
	return live
}

// coverRoundtrip sends one secret-cover and waits for the reply. The reply is
// the confirmation: a sibling that never answers reads as uncovered, never as
// covered. A cover that deadlocks against a sibling whose own push waits on
// this server unwinds here, on the deadline, and reads as uncovered, the safe
// side.
func coverRoundtrip(c net.Conn, own string, values []string) error {
	body, err := json.Marshal(map[string]any{"session": own, "values": values})
	if err != nil {
		return err
	}
	_ = c.SetDeadline(time.Now().Add(coverRoundtripTimeout))
	if err := wire.WriteMsg(c, wire.Msg{Type: "req", Action: "secret-cover", ID: 1, Res: json.RawMessage(body)}); err != nil {
		return err
	}
	rep, err := wire.ReadMsg(c)
	if err != nil {
		return err
	}
	if !rep.OK {
		if rep.Error == "" {
			return errors.New("cover refused")
		}
		return errors.New(rep.Error)
	}
	return nil
}

// secretBoundsWarning returns one line when the pane stage cannot redact a
// value: too short to match safely, or too long. The line names the secret
// and its length, never the value.
func secretBoundsWarning(name, value string) string {
	switch {
	case len(value) < pane.MinSecretLen:
		return fmt.Sprintf("secret %s: value is %d bytes; panes do not redact values under %d bytes, so it will echo unredacted", name, len(value), pane.MinSecretLen)
	case len(value) > pane.MaxSecretLen:
		return fmt.Sprintf("secret %s: value is %d bytes; panes do not redact values over %d bytes, so it will echo unredacted", name, len(value), pane.MaxSecretLen)
	}
	return ""
}

// secretCapWarning names the secrets the cap left off the panes' lists, so
// the operator knows exactly which values ride unredacted. The cut is
// deterministic: the panes keep the first MaxSecrets names in name order.
func secretCapWarning(dropped []string) string {
	if len(dropped) == 0 {
		return ""
	}
	shown := dropped
	if len(shown) > 5 {
		shown = shown[:5]
	}
	more := ""
	if len(dropped) > 5 {
		more = fmt.Sprintf(" and %d more", len(dropped)-5)
	}
	return fmt.Sprintf("the panes hold %d values; these ride unredacted until some are revoked: %s%s",
		pane.MaxSecrets, strings.Join(shown, ", "), more)
}

// secretRegister adds one secret to the session's registry. The registry
// stores how the value is fetched and where it rides, never the value.
func (s *server) secretRegister(cl *client, m wire.Msg) {
	sec := &session.Secret{
		Name:      m.Name,
		Tool:      m.Tool,
		Reference: m.Ref,
		Upstream:  m.Upstream,
		Header:    m.Header,
		Env:       m.Env,
	}
	if err := sec.Validate(); err != nil {
		s.reply(cl, m, nil, err)
		return
	}
	// The proxy shape is checked here, not at use time: a bad header name or
	// a one-sided pairing must not cost a fetch first.
	if (sec.Upstream == "") != (sec.Header == "") {
		s.reply(cl, m, nil, fmt.Errorf("secret %s: --upstream and --header go together", sec.Name))
		return
	}
	if sec.Upstream != "" {
		if err := proxy.Validate(sec.Upstream, sec.Header); err != nil {
			s.reply(cl, m, nil, fmt.Errorf("secret %s: %w", sec.Name, err))
			return
		}
	}
	err := s.mutation(func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.st.Secret(sec.Name) != nil {
			return fmt.Errorf("secret %q is already registered", sec.Name)
		}
		s.st.Secrets = append(s.st.Secrets, sec)
		return nil
	})
	if err == nil {
		s.reply(cl, m, map[string]any{"name": sec.Name}, nil)
		return
	}
	s.reply(cl, m, nil, err)
}

// secretRow is one secret list row: the name and the reference it resolves
// through, nothing else. The upstream and header shape the value's route and
// are not part of the list answer.
type secretRow struct {
	Name      string `json:"name"`
	Reference string `json:"reference"`
}

// secretList prints the registry: names and references only.
func (s *server) secretList(cl *client, m wire.Msg) {
	s.mu.Lock()
	rows := make([]secretRow, 0, len(s.st.Secrets))
	for _, sec := range s.st.Secrets {
		rows = append(rows, secretRow{Name: sec.Name, Reference: sec.Reference})
	}
	s.mu.Unlock()
	s.reply(cl, m, map[string]any{"secrets": rows}, nil)
}

// secretRevoke removes a secret from the registry and drops every live copy
// of its value: the server registry, the proxy prefix and the panes'
// redaction lists. A state save carries the removal; the value was never in
// it.
func (s *server) secretRevoke(cl *client, m wire.Msg) {
	name := m.Name
	err := s.mutation(func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		for i, sec := range s.st.Secrets {
			if sec.Name != name {
				continue
			}
			last := len(s.st.Secrets) - 1
			s.st.Secrets[i] = s.st.Secrets[last]
			s.st.Secrets[last] = nil
			s.st.Secrets = s.st.Secrets[:last]
			return nil
		}
		return fmt.Errorf("no secret %q", name)
	})
	if err != nil {
		s.reply(cl, m, nil, err)
		return
	}
	s.secretDeactivate(name)
	s.reply(cl, m, map[string]any{"name": name}, nil)
}

// secretDeactivate drops one secret's live value and proxy prefix, then
// pushes the remaining values to the panes. Unknown names are ignored, so a
// revoke of a never-used secret still cleans the panes' list.
func (s *server) secretDeactivate(name string) {
	s.mu.Lock()
	s.secretEnsure()
	s.secrets.Drop(name)
	delete(s.secretActive, name)
	prefix := s.secretPrefixes[name]
	delete(s.secretPrefixes, name)
	p := s.proxy
	s.mu.Unlock()
	if p != nil && prefix != "" {
		p.Revoke(prefix)
	}
	s.pushSecretScrub()
}

// secretUse fetches the value, activates it everywhere it is needed and
// answers with the endpoint URL alone. With an upstream the value rides the
// proxy under a fresh random prefix; the old prefix, if any, dies. Either
// way every pane's redaction list learns the value before the reply leaves.
func (s *server) secretUse(cl *client, m wire.Msg) {
	sec := s.secretLookup(m.Name)
	if sec == nil {
		s.reply(cl, m, nil, fmt.Errorf("no secret %q", m.Name))
		return
	}
	value, err := s.fetchSecret(sec)
	if err != nil {
		s.reply(cl, m, nil, err)
		return
	}
	// The activation checks identity under the lock: a revoke that raced
	// the fetch wins, and a revoke-and-re-register does not pair the old
	// value with the new record. Nothing of the fetched value stays behind.
	if err := s.secretActivate(sec, value); err != nil {
		s.reply(cl, m, nil, err)
		return
	}
	endpoint := ""
	// The live record decides the ride: a revoke-and-re-register during the
	// fetch may have changed the shape under the same name.
	if now := s.secretLookup(sec.Name); now != nil && now.Upstream != "" {
		prefix, err := s.secretProxyRegister(sec.Name, value)
		if err != nil {
			s.reply(cl, m, nil, err)
			return
		}
		endpoint = "http://" + s.secretProxyAddr() + prefix
	}
	failed := s.pushSecretScrub()
	uncovered := make([]string, 0, len(failed))
	for id := range failed {
		uncovered = append(uncovered, id)
	}
	sort.Strings(uncovered)
	for id, err := range failed {
		log.Printf("pane %s: set-secrets: %v", id, err)
	}
	var warnings []string
	if w := secretBoundsWarning(sec.Name, value); w != "" {
		warnings = append(warnings, w)
	}
	if _, dropped := s.secretActiveSet(); len(dropped) > 0 {
		warnings = append(warnings, secretCapWarning(dropped))
	}
	warning := strings.Join(warnings, "; ")
	for _, w := range warnings {
		log.Print(w)
	}
	if s.afterActivate != nil {
		s.afterActivate()
	}
	// The reply must not call a revoked secret a success. One last look
	// under the lock, so a revoke that landed anywhere between the
	// activation and this reply, during the fetch or during the push,
	// reads as the refusal it is.
	s.mu.Lock()
	gone := s.st.Secret(sec.Name) == nil
	s.mu.Unlock()
	if gone {
		s.reply(cl, m, nil, fmt.Errorf("secret %q was revoked while the value was being fetched or pushed; run dm secret use again", sec.Name))
		return
	}
	s.reply(cl, m, map[string]any{"name": sec.Name, "endpoint": endpoint, "warning": warning, "uncovered": uncovered}, nil)
}

// secretActivate stores a fetched value and marks it live. It checks the
// record's identity, not just the name: a revoke that raced the fetch wins
// and leaves nothing behind, and a revoke-and-re-register during the fetch
// refuses to pair the old value with the new record's shape.
func (s *server) secretActivate(sec *session.Secret, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.secretEnsure()
	if s.st.Secret(sec.Name) != sec {
		return fmt.Errorf("secret %q changed while the value was being fetched; run dm secret use again", sec.Name)
	}
	s.secrets.Put(sec.Name, value)
	s.secretActive[sec.Name] = true
	return nil
}

// secretActiveSet is the live values with the names the cap dropped, under
// s.mu.
func (s *server) secretActiveSet() ([]string, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.secretValuesLocked()
}

// secretLookup returns the registered secret by name.
func (s *server) secretLookup(name string) *session.Secret {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.Secret(name)
}

// secretProxyRegister starts the loopback proxy on first use, mints a fresh
// prefix for the value and retires the prefix a previous use minted. It runs
// wholly under s.mu: the register, the rotation and the map write are one
// step, so a revoke that races a use can never leave an untracked live
// prefix. Register is memory only, so the hold is short.
func (s *server) secretProxyRegister(name, value string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.secretEnsure()
	if s.st.Secret(name) == nil {
		return "", fmt.Errorf("no secret %q", name)
	}
	sec := s.st.Secret(name)
	if s.proxy == nil {
		p, err := proxy.New(nil)
		if err != nil {
			return "", err
		}
		s.proxy = p
		go func() { _ = p.Serve() }()
	}
	prefix, err := s.proxy.Register(sec.Name, sec.Upstream, sec.Header, value)
	if err != nil {
		return "", err
	}
	if old := s.secretPrefixes[sec.Name]; old != "" && old != prefix {
		s.proxy.Revoke(old)
	}
	s.secretPrefixes[sec.Name] = prefix
	return prefix, nil
}

// secretProxyAddr is the proxy's listen address, "127.0.0.1:port".
func (s *server) secretProxyAddr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.proxy == nil {
		return ""
	}
	return s.proxy.Addr()
}

// secretHydrate fetches the value, activates it, pushes the redaction set and
// writes the value to a file at 0600. Activation rides the fetch because a
// hydrate-only flow must still leave every pane scrubbing: the
// register-before-use rule does not ask how the value will be used. The
// hydrate never starts the proxy: the file is the ride, and the reply names
// the path alone. A failed write leaves the value active: the panes already
// scrub it, and a revoke cleans up what a retry does not.
func (s *server) secretHydrate(cl *client, m wire.Msg) {
	if m.Path == "" {
		s.reply(cl, m, nil, fmt.Errorf("secret hydrate needs --to"))
		return
	}
	sec := s.secretLookup(m.Name)
	if sec == nil {
		s.reply(cl, m, nil, fmt.Errorf("no secret %q", m.Name))
		return
	}
	value, err := s.fetchSecret(sec)
	if err != nil {
		s.reply(cl, m, nil, err)
		return
	}
	// The same identity check a use runs: a revoke that raced the fetch
	// wins, and a revoke-and-re-register does not pair the old value with
	// the new record's shape.
	if err := s.secretActivate(sec, value); err != nil {
		s.reply(cl, m, nil, err)
		return
	}
	failed := s.pushSecretScrub()
	for id, err := range failed {
		log.Printf("pane %s: set-secrets: %v", id, err)
	}
	uncovered := make([]string, 0, len(failed))
	for id := range failed {
		uncovered = append(uncovered, id)
	}
	sort.Strings(uncovered)
	if err := secrets.Hydrate(m.Path, value); err != nil {
		s.reply(cl, m, nil, err)
		return
	}
	var warnings []string
	if w := secretBoundsWarning(sec.Name, value); w != "" {
		warnings = append(warnings, w)
	}
	if _, dropped := s.secretActiveSet(); len(dropped) > 0 {
		warnings = append(warnings, secretCapWarning(dropped))
	}
	if len(uncovered) > 0 {
		warnings = append(warnings, fmt.Sprintf("the redaction set did not reach: %s; the value would echo there unredacted", strings.Join(uncovered, ", ")))
	}
	warning := strings.Join(warnings, "; ")
	for _, w := range warnings {
		log.Print(w)
	}
	s.reply(cl, m, map[string]any{"path": m.Path, "warning": warning, "uncovered": uncovered}, nil)
}

// secretCover stores another session's live values and pushes the merged set
// to this session's panes. The sender owns the values: it pushes every change
// and the withdrawal, an empty set, and this server folds them into every
// push its panes hear. The values ride memory only, like every secret value:
// never the state file, the logs or a reply. The push runs before the reply,
// so a sender that heard the count knows the panes already scrub.
func (s *server) secretCover(cl *client, m wire.Msg) {
	var body struct {
		Session string   `json:"session"`
		Values  []string `json:"values"`
	}
	if err := json.Unmarshal(m.Res, &body); err != nil || body.Session == "" {
		s.reply(cl, m, nil, fmt.Errorf("secret-cover needs a session and a set of values"))
		return
	}
	if !paths.ValidSessionName(body.Session) || body.Session == s.st.Name {
		s.reply(cl, m, nil, fmt.Errorf("secret-cover refuses session %q", body.Session))
		return
	}
	s.mu.Lock()
	s.secretEnsure()
	if len(body.Values) == 0 {
		delete(s.secretForeign, body.Session)
	} else {
		s.secretForeign[body.Session] = body.Values
	}
	s.mu.Unlock()
	failed := s.pushLocalScrub()
	s.mu.Lock()
	panes := len(s.panes)
	s.mu.Unlock()
	s.reply(cl, m, map[string]any{"panes": panes - len(failed)}, nil)
}

// secretType fetches the value and types it into a pane through the existing
// write-to path. The redaction push lands first, and it must reach every pane
// the keystrokes reach: a synced write fans out to the whole window, and a
// pane that never learned the value would echo it unredacted to every client
// and into its ring, so the type refuses instead. The warning is computed
// before the write, so the operator hears about an unredactable value before
// it echoes.
func (s *server) secretType(cl *client, m wire.Msg) {
	sec := s.secretLookup(m.Name)
	if sec == nil {
		s.reply(cl, m, nil, fmt.Errorf("no secret %q", m.Name))
		return
	}
	value, err := s.fetchSecret(sec)
	if err != nil {
		s.reply(cl, m, nil, err)
		return
	}
	if err := s.secretActivate(sec, value); err != nil {
		s.reply(cl, m, nil, err)
		return
	}
	failed := s.pushSecretScrub()
	for id, err := range failed {
		if id != m.Target {
			log.Printf("pane %s: set-secrets: %v", id, err)
		}
	}
	var missed []string
	for _, id := range s.secretTypeDests(m.Target) {
		if err, ok := failed[id]; ok {
			missed = append(missed, fmt.Sprintf("%s (%v)", id, err))
		}
	}
	if len(missed) > 0 {
		s.reply(cl, m, nil, fmt.Errorf("panes %s did not accept the redaction set; the value was not typed", strings.Join(missed, ", ")))
		return
	}
	warning := secretBoundsWarning(sec.Name, value)
	if warning != "" {
		log.Print(warning)
	}
	if err := s.writeTo(m.Target, value, false); err != nil {
		s.reply(cl, m, nil, err)
		return
	}
	s.reply(cl, m, map[string]any{"pane": m.Target, "warning": warning}, nil)
}

// secretTypeDests names the panes the keystrokes will reach: the whole
// window when it is synced, the target alone otherwise.
func (s *server) secretTypeDests(id string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if dsts := s.syncPaneIDsLocked(id); len(dsts) > 0 {
		return dsts
	}
	return []string{id}
}
