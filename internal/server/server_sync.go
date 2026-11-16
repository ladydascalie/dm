package server

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"dm/internal/kdl"
	"dm/internal/pane"
	"dm/internal/session"
	"dm/internal/wire"
)

// The secret sync action consumes a .dm-secrets.kdl mapping. Every secret
// block registers the way secret register does, idempotent on a name the
// registry already holds. Every file block is read and each non-empty line
// donates everything after the first = as a redaction value: the values
// activate into the registry, the push lands before the reply, and the reply
// carries the same warning and uncovered shape the other secret verbs
// answer with. A file block never fetches and never rides the proxy: its
// values are display protection only. A value read from a file lives in the
// registry and the redaction push alone; it never reaches argv, the logs,
// the state file, the box or a reply body. A file key is file data too: the
// replies and the logs carry counts and the file's path, never a key, because
// a key can hold any text, a value included.

// mappingSecret is one secret block of a parsed mapping.
type mappingSecret struct {
	Name     string
	Tool     string
	Ref      string
	Upstream string
	Header   string
	Env      []string
	Line     int
}

// mappingFile is one file block of a parsed mapping.
type mappingFile struct {
	Path string
	Line int
}

// secretsMapping is the parsed .dm-secrets.kdl: the secret blocks and the
// file blocks, in file order.
type secretsMapping struct {
	Secrets []mappingSecret
	Files   []mappingFile
}

// parseSecretsMapping reads one mapping text with the kdl tokenizer. The
// shape is fixed: a secret node takes one quoted name and carries tool, ref,
// env (repeatable), upstream and header children, each one quoted value; a
// file node takes one quoted path and carries nothing. Anything else is an
// error naming the line.
func parseSecretsMapping(src string) (secretsMapping, error) {
	toks, err := kdl.Scan(src)
	if err != nil {
		return secretsMapping{}, err
	}
	var mp secretsMapping
	seenSecret := map[string]bool{}
	seenFile := map[string]bool{}
	i := 0
	for {
		if i >= len(toks) || toks[i].Kind == kdl.EOF {
			return mp, nil
		}
		t := toks[i]
		if t.Kind != kdl.Ident {
			return secretsMapping{}, fmt.Errorf("line %d: expected a node name, got %q", t.Line, t.Value)
		}
		i++
		args, i2, err := scanArgs(toks, i, t.Value)
		if err != nil {
			return secretsMapping{}, err
		}
		i = i2
		var kids []kdl.Token
		if i < len(toks) && toks[i].Kind == kdl.LBrace {
			i++
			for {
				if i >= len(toks) || toks[i].Kind == kdl.EOF {
					return secretsMapping{}, fmt.Errorf("line %d: %s block is not closed", t.Line, t.Value)
				}
				if toks[i].Kind == kdl.RBrace {
					i++
					break
				}
				kt := toks[i]
				if kt.Kind != kdl.Ident {
					return secretsMapping{}, fmt.Errorf("line %d: expected a node name inside %s, got %q", kt.Line, t.Value, kt.Value)
				}
				i++
				kargs, i2, err := scanArgs(toks, i, kt.Value)
				if err != nil {
					return secretsMapping{}, err
				}
				i = i2
				if i < len(toks) && toks[i].Kind == kdl.LBrace {
					return secretsMapping{}, fmt.Errorf("line %d: %s takes no block inside %s", kt.Line, kt.Value, t.Value)
				}
				kids = append(kids, kt)
				kids = append(kids, kargs...)
			}
		}
		switch t.Value {
		case "secret":
			if len(args) != 1 {
				return secretsMapping{}, fmt.Errorf("line %d: secret takes exactly one quoted name", t.Line)
			}
			b := mappingSecret{Name: args[0].Value, Line: t.Line}
			for j := 0; j < len(kids); {
				key := kids[j]
				vals := kids[j+1 : j+1+countStrings(kids, j+1)]
				j += 1 + len(vals)
				if len(vals) != 1 {
					return secretsMapping{}, fmt.Errorf("line %d: %s takes exactly one quoted value inside secret", key.Line, key.Value)
				}
				v := vals[0].Value
				switch key.Value {
				case "tool":
					b.Tool = v
				case "ref":
					b.Ref = v
				case "env":
					b.Env = append(b.Env, v)
				case "upstream":
					b.Upstream = v
				case "header":
					b.Header = v
				default:
					return secretsMapping{}, fmt.Errorf("line %d: unknown %s inside secret %s", key.Line, key.Value, b.Name)
				}
			}
			if seenSecret[b.Name] {
				return secretsMapping{}, fmt.Errorf("line %d: secret %s appears twice", t.Line, b.Name)
			}
			seenSecret[b.Name] = true
			mp.Secrets = append(mp.Secrets, b)
		case "file":
			if len(args) != 0 {
				return secretsMapping{}, fmt.Errorf("line %d: file takes no node arguments; put the path in a path child", t.Line)
			}
			if len(kids) != 2 || kids[0].Value != "path" {
				return secretsMapping{}, fmt.Errorf("line %d: file takes exactly one path child: path \".env\"", t.Line)
			}
			p := kids[1].Value
			if seenFile[p] {
				return secretsMapping{}, fmt.Errorf("line %d: file %s appears twice", t.Line, p)
			}
			seenFile[p] = true
			mp.Files = append(mp.Files, mappingFile{Path: p, Line: t.Line})
		default:
			return secretsMapping{}, fmt.Errorf("line %d: unknown node %q: want secret or file", t.Line, t.Value)
		}
	}
}

// scanArgs collects the quoted arguments a node carries and returns the
// index of the first token that is not one. An identifier that does not
// start its line is a bare value and is refused; one that does is the next
// node or the first child, and the caller reads it. The rule is Bol, not a
// line comparison: kdl.Scan stamps a multiline string with its opening
// line, so a line comparison would take an identifier on the closing
// quote's line for the next node.
func scanArgs(toks []kdl.Token, i int, node string) ([]kdl.Token, int, error) {
	var args []kdl.Token
	for i < len(toks) && toks[i].Kind == kdl.String {
		args = append(args, toks[i])
		i++
	}
	if i < len(toks) && toks[i].Kind == kdl.Ident && !toks[i].Bol {
		return nil, i, fmt.Errorf("line %d: %s takes quoted values only, got %q", toks[i].Line, node, toks[i].Value)
	}
	return args, i, nil
}

// countStrings counts the String tokens from index i, stopping at the first
// token of another kind.
func countStrings(toks []kdl.Token, i int) int {
	n := 0
	for i+n < len(toks) && toks[i+n].Kind == kdl.String {
		n++
	}
	return n
}

// fileResult is one file block's answer: the counts the line rule produced
// and the read error, if any. No value and no file key ever appears here; a
// key is untrusted file data and can equal the value beside it.
type fileResult struct {
	Path     string `json:"path"`
	Values   int    `json:"values"`
	Skipped  int    `json:"skipped"`
	Empty    int    `json:"empty"`
	EmptyKey int    `json:"emptyKeys,omitempty"`
	Repeated int    `json:"repeatedKeys,omitempty"`
	Bounds   int    `json:"bounds"`
	Error    string `json:"error,omitempty"`
}

// maxSecretFileBytes caps one mapping or file block read. The mapping is
// untrusted input: a device, a FIFO or a huge regular file must not grow the
// server's heap or stall the sync request, so every read asks for at most
// this much and refuses a regular file bigger than it.
const maxSecretFileBytes = 1 << 20

// configDir is the operator's dm config directory: $XDG_CONFIG_HOME/dm, or
// ~/.config/dm when XDG_CONFIG_HOME is unset. Empty when the home directory is
// unknown.
func configDir() string {
	home, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "dm")
}

// mappingUnderConfigDir reports whether path resolves under the config
// directory, symlinks on both sides included. dm exec sends no trust verdict,
// so the server answers with this: a mapping the operator keeps in the config
// tree is trusted, a mapping in a repo is not.
func mappingUnderConfigDir(path string) bool {
	dir := configDir()
	if dir == "" {
		return false
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		realDir = filepath.Clean(dir)
	}
	realPath, err := filepath.EvalSymlinks(path)
	if err != nil {
		realPath = filepath.Clean(path)
	}
	rel, err := filepath.Rel(realDir, realPath)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// secretSyncTrusted resolves the mapping's trust verdict: an explicit wire
// verdict wins, and nil defers to the mapping's path. dm secret sync always
// sends its verdict; a raw dm exec body carries none.
func secretSyncTrusted(trust *bool, path string) bool {
	if trust != nil {
		return *trust
	}
	return mappingUnderConfigDir(path)
}

// secretSync applies one mapping: register the secret blocks, ingest the
// file blocks, push the redaction set and answer. The mapping path arrives
// absolute; the file paths inside it resolve against its directory.
//
// The sender may carry a trust verdict: dm secret sync sends true when the
// mapping lives under the operator's config directory or the operator passed
// --trust, and false otherwise. A nil verdict, which is what a raw dm exec
// body carries, makes the server resolve the same question from the mapping's
// path. An untrusted mapping may not register secret blocks and may read only
// files under its own directory: a repo-local mapping must not pin an
// upstream for the operator's store value or read the operator's files.
func (s *server) secretSync(cl *client, m wire.Msg) {
	if m.Path == "" {
		s.reply(cl, m, nil, fmt.Errorf("secret sync needs the mapping path"))
		return
	}
	src, err := readRegularFile(m.Path, maxSecretFileBytes)
	if err != nil {
		s.reply(cl, m, nil, fmt.Errorf("secret sync: %w", err))
		return
	}
	mp, err := parseSecretsMapping(string(src))
	if err != nil {
		s.reply(cl, m, nil, fmt.Errorf("%s: %w", m.Path, err))
		return
	}
	trusted := secretSyncTrusted(m.Trust, m.Path)
	if !trusted && len(mp.Secrets) > 0 {
		s.reply(cl, m, nil, fmt.Errorf(
			"%s: an untrusted mapping registers no secrets; move it under $XDG_CONFIG_HOME/dm (or ~/.config/dm) or run dm secret sync --trust",
			m.Path))
		return
	}
	// Every secret block validates before anything mutates, so a bad block
	// costs nothing and registers nothing.
	specs := make([]*session.Secret, 0, len(mp.Secrets))
	for i := range mp.Secrets {
		b := &mp.Secrets[i]
		sec := &session.Secret{
			Name: b.Name, Tool: b.Tool, Reference: b.Ref,
			Upstream: b.Upstream, Header: b.Header, Env: b.Env,
		}
		if err := checkSecretShape(sec); err != nil {
			s.reply(cl, m, nil, fmt.Errorf("%s line %d: %w", m.Path, b.Line, err))
			return
		}
		specs = append(specs, sec)
	}
	registered, kept := []string{}, []string{}
	err = s.mutation(func() error {
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, sec := range specs {
			if s.st.Secret(sec.Name) != nil {
				kept = append(kept, sec.Name)
				continue
			}
			s.st.Secrets = append(s.st.Secrets, sec)
			registered = append(registered, sec.Name)
		}
		return nil
	})
	if err != nil {
		s.reply(cl, m, nil, err)
		return
	}
	dir := filepath.Dir(m.Path)
	realDir := dir
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		realDir = resolved
	}
	results := make([]fileResult, 0, len(mp.Files))
	for i := range mp.Files {
		results = append(results, s.ingestSecretFile(fileScan{
			dir: dir, realDir: realDir, block: mp.Files[i], trusted: trusted,
		}))
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
	for _, r := range results {
		if r.Error != "" {
			warnings = append(warnings, fmt.Sprintf("file %s: %s", r.Path, r.Error))
			continue
		}
		var lost []string
		if r.Skipped > 0 {
			lost = append(lost, fmt.Sprintf("%d lines without =", r.Skipped))
		}
		if r.Empty > 0 {
			lost = append(lost, fmt.Sprintf("%d empty values", r.Empty))
		}
		if r.EmptyKey > 0 {
			lost = append(lost, fmt.Sprintf("%d empty keys", r.EmptyKey))
		}
		if len(lost) > 0 {
			warnings = append(warnings, fmt.Sprintf("file %s: %s; they registered nothing", r.Path, strings.Join(lost, ", ")))
		}
		if r.Repeated > 0 {
			warnings = append(warnings, fmt.Sprintf("file %s: %d keys repeat; only their last value redacts", r.Path, r.Repeated))
		}
		if r.Bounds > 0 {
			warnings = append(warnings, fmt.Sprintf("file %s: %d values are outside the %d..%d byte redaction range; they registered nothing and will echo unredacted", r.Path, r.Bounds, pane.MinSecretLen, pane.MaxSecretLen))
		}
	}
	_, dropped, droppedFiles := s.secretActiveSet()
	if len(dropped) > 0 {
		warnings = append(warnings, secretCapWarning(dropped))
	}
	if w := secretFileCapWarning(droppedFiles); w != "" {
		warnings = append(warnings, w)
	}
	if len(uncovered) > 0 {
		warnings = append(warnings, fmt.Sprintf("the redaction set did not reach: %s; the values would echo there unredacted", strings.Join(uncovered, ", ")))
	}
	warning := strings.Join(warnings, "; ")
	for _, w := range warnings {
		log.Print(w)
	}
	s.reply(cl, m, map[string]any{
		"mapping": m.Path, "registered": registered, "kept": kept,
		"files": results, "warning": warning, "uncovered": uncovered,
	}, nil)
}

// fileScan is one file block to read: the mapping directory it resolves
// against, that directory with symlinks resolved, the block itself and the
// mapping's trust verdict.
type fileScan struct {
	dir     string
	realDir string
	block   mappingFile
	trusted bool
}

// ingestSecretFile reads one file block's path and turns its lines into
// redaction values. A non-empty line donates everything after the first =;
// a line without = registers nothing; an empty value, an empty key and a
// value outside the scrub bounds register nothing and are counted. A key
// the file already donated keeps its last in-bounds donation: the earlier
// ones drop, and every key that dropped one is counted, so a collision never
// shrinks the protected set in silence. The count travels out, never the key:
// a key is file data and can equal the value beside it. The values activate
// under s.mu with names derived from the path and the key, so a re-run of the
// same mapping replaces exactly the values it donated before and nothing
// else. The counts travel out, the values never do.
//
// An untrusted block may read only a regular file under the mapping's own
// directory. A trusted block may read anywhere the operator can, as before.
func (s *server) ingestSecretFile(scan fileScan) fileResult {
	path := scan.block.Path
	if !filepath.IsAbs(path) {
		path = filepath.Join(scan.dir, path)
	}
	res := fileResult{Path: path}
	if !scan.trusted {
		if err := checkFileConfined(scan.realDir, scan.block.Path, path); err != nil {
			res.Error = err.Error()
			return res
		}
	}
	raw, err := readRegularFile(path, maxSecretFileBytes)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	values := map[string]string{}
	repeated := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		key, value, found := strings.Cut(line, "=")
		if !found {
			res.Skipped++
			continue
		}
		if key == "" {
			res.EmptyKey++
			continue
		}
		if value == "" {
			res.Empty++
			continue
		}
		if len(value) < pane.MinSecretLen || len(value) > pane.MaxSecretLen {
			res.Bounds++
			continue
		}
		name := path + "#" + key
		if _, dup := values[name]; dup {
			repeated[key] = true
		}
		values[name] = value
	}
	res.Values = len(values)
	res.Repeated = len(repeated)
	// The activation is one step under s.mu, so a concurrent push reads
	// either the old set or the new one, never a half-swapped mixture.
	s.mu.Lock()
	s.secretEnsure()
	old := s.secretFiles[path]
	for _, name := range old {
		s.secrets.Drop(name)
		delete(s.secretActive, name)
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		s.secrets.Put(name, values[name])
		s.secretActive[name] = true
	}
	s.secretFiles[path] = names
	s.mu.Unlock()
	return res
}

// checkFileConfined refuses a file block that resolves outside the mapping's
// own directory. Symlinks resolve on both sides, so a link placed in the
// mapping's directory cannot point the read out of it, and an absolute path
// inside the directory still passes. The refusal names the block's declared
// path, the form the mapping's author wrote.
func checkFileConfined(realDir, declared, path string) error {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(realDir, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("path %q resolves outside the mapping directory %s; an untrusted mapping reads only files under its own directory", declared, realDir)
	}
	return nil
}

// readRegularFile reads a regular file of at most limit bytes and refuses
// anything else by name. The stat runs before the open, so a FIFO, a device
// or a directory never stalls or grows the read, and the limit reader holds
// the bytes in case the file grows past the size the stat saw.
func readRegularFile(path string, limit int64) ([]byte, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", path)
	}
	if fi.Size() > limit {
		return nil, fmt.Errorf("%s is %d bytes; the limit is %d", path, fi.Size(), limit)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("%s grew past the %d byte limit", path, limit)
	}
	return raw, nil
}
