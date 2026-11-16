package server

import (
	"fmt"
	"log"
	"strings"

	"dm/internal/proxy"
	"dm/internal/secrets"
	"dm/internal/session"
	"dm/internal/wire"
)

// The secret actions move a fetched value through exactly four places: the
// secrets registry, the proxy's upstream request, the keystroke burst a
// secret type sends and the redaction registries the panes hold. The value
// never reaches argv, the logs, the state file, the box or a reply body:
// every reply names the secret, never the value.

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
}

// secretTool maps a registered secret to the command that prints its value.
// The reference is an argument, never a value; the child env is exactly what
// the tool needs and nothing else.
func secretTool(sec *session.Secret) secrets.Tool {
	switch sec.Tool {
	case session.ToolOp:
		return secrets.Tool{Name: "op", Bin: "op", Args: []string{"read", sec.Reference}}
	case session.ToolPass:
		return secrets.Tool{Name: "pass", Bin: "pass", Args: []string{"show", sec.Reference}}
	}
	return secrets.Tool{Name: sec.Tool}
}

// fetchSecret runs the secret's tool and returns the value. The stub field
// keeps tests off PATH.
func (s *server) fetchSecret(sec *session.Secret) (string, error) {
	if s.fetch != nil {
		return s.fetch(sec)
	}
	return secrets.Fetch(secretTool(sec))
}

// secretValues returns the live values in registry order, under s.mu.
func (s *server) secretValues() []string {
	s.secretEnsure()
	var values []string
	for name := range s.secretActive {
		if v, ok := s.secrets.Get(name); ok {
			values = append(values, v)
		}
	}
	return values
}

// pushSecretScrub sends the live values to every adopted pane, so their
// output stage redacts each one. It runs off s.mu: a pane send writes on its
// own connection and never needs the server lock.
func (s *server) pushSecretScrub() {
	s.mu.Lock()
	values := s.secretValues()
	panes := make([]*paneConn, 0, len(s.panes))
	for _, pc := range s.panes {
		panes = append(panes, pc)
	}
	s.mu.Unlock()
	// An empty push is the point of a revoke: the panes must hear the set
	// is gone, not stay with a stale value on their list.
	body := jsonRaw(map[string]any{"values": values})
	for _, pc := range panes {
		if err := pc.send(wire.Msg{Action: "set-secrets", Res: body}); err != nil {
			log.Printf("pane %s: set-secrets: %v", pc.id, err)
		}
	}
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
	}
	if err := sec.Validate(); err != nil {
		s.reply(cl, m, nil, err)
		return
	}
	if err := checkHeaderTemplate(sec.Header); err != nil {
		s.reply(cl, m, nil, fmt.Errorf("secret %s: %w", sec.Name, err))
		return
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

// checkHeaderTemplate mirrors the proxy's own template rules early, at
// register time: exactly one {} slot, a "Name: value" colon, no CR or LF.
// The proxy validates again with the value in hand.
func checkHeaderTemplate(template string) error {
	if template == "" {
		return nil
	}
	if !strings.Contains(template, ":") {
		return fmt.Errorf("header template %q needs %q form", template, "Name: value with {}")
	}
	if strings.Count(template, "{}") != 1 {
		return fmt.Errorf("header template %q needs exactly one {} slot", template)
	}
	if strings.ContainsAny(template, "\r\n") {
		return fmt.Errorf("header template must not contain CR or LF")
	}
	return nil
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
	s.mu.Lock()
	s.secretEnsure()
	s.secrets.Put(sec.Name, value)
	s.secretActive[sec.Name] = true
	s.mu.Unlock()

	endpoint := ""
	if sec.Upstream != "" {
		prefix, err := s.secretProxyRegister(sec, value)
		if err != nil {
			s.reply(cl, m, nil, err)
			return
		}
		endpoint = "http://" + s.secretProxyAddr() + prefix
	}
	s.pushSecretScrub()
	s.reply(cl, m, map[string]any{"name": sec.Name, "endpoint": endpoint}, nil)
}

// secretLookup returns the registered secret by name.
func (s *server) secretLookup(name string) *session.Secret {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.Secret(name)
}

// secretProxyRegister starts the loopback proxy on first use, mints a fresh
// prefix for the value and retires the prefix a previous use minted.
func (s *server) secretProxyRegister(sec *session.Secret, value string) (string, error) {
	s.mu.Lock()
	s.secretEnsure()
	if s.proxy == nil {
		p, err := proxy.New(nil)
		if err != nil {
			s.mu.Unlock()
			return "", err
		}
		s.proxy = p
		go func() { _ = p.Serve() }()
	}
	p := s.proxy
	old := s.secretPrefixes[sec.Name]
	s.mu.Unlock()

	prefix, err := p.Register(sec.Name, sec.Upstream, sec.Header, value)
	if err != nil {
		return "", err
	}
	if old != "" && old != prefix {
		p.Revoke(old)
	}
	s.mu.Lock()
	s.secretPrefixes[sec.Name] = prefix
	s.mu.Unlock()
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

// secretHydrate fetches the value and writes it to a file at 0600. The file
// write is out of band: the reply names the path, never the value.
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
	if err := secrets.Hydrate(m.Path, value); err != nil {
		s.reply(cl, m, nil, err)
		return
	}
	s.reply(cl, m, map[string]any{"path": m.Path}, nil)
}

// secretType fetches the value and types it into a pane through the existing
// write-to path. The redaction push lands first, so the echo the pane prints
// is scrubbed like any other output that carries the value.
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
	s.mu.Lock()
	s.secretEnsure()
	s.secrets.Put(sec.Name, value)
	s.secretActive[sec.Name] = true
	s.mu.Unlock()
	s.pushSecretScrub()
	if err := s.writeTo(m.Target, value, false); err != nil {
		s.reply(cl, m, nil, err)
		return
	}
	s.reply(cl, m, map[string]any{"pane": m.Target}, nil)
}
