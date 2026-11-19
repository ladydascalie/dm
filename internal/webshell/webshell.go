// Package webshell is the pretend shell a browser pane runs. There is no
// process under it and no pty: it turns keys into the bytes a shell would
// print, over a small file tree the commands agree with.
//
// It exists for the tour at cmd/dm-wasm. A command that is not here is refused
// the way a shell refuses it, because a half working command reads as broken
// where a refusal reads as a limit.
package webshell

import (
	"path"
	"strings"
)

const (
	// Home is the session's home directory.
	Home = "/home/guest"
	// Repo is the one project on disk, and the one repository.
	Repo = Home + "/projects/hello"
)

// prompt is what the shell prints before every line.
const prompt = "\x1b[32mguest@dm\x1b[0m:\x1b[34m%s\x1b[0m$ "

// Shell is a line editor over a small file tree.
type Shell struct {
	cwd  string
	line []rune
	// esc is where a key is in an escape sequence: 0 outside one, 1 after the
	// esc, 2 inside a CSI. A sequence is swallowed, so an arrow key does not
	// leave its tail on the line. Up and down are the two it acts on.
	esc int
	// history holds the lines that ran, oldest first. at indexes it while
	// walking: len(history) means the line being typed, and draft keeps that
	// line so down can come back to it.
	history []string
	at      int
	draft   []rune
	// scroll is what the screen holds, one entry per line, prompt included.
	// The pane redraws from it when the window changes size, because a
	// terminal cannot rewrap bytes it already printed: a narrower pane would
	// otherwise clip the right half of everything and keep it clipped.
	scroll []string
	// OnRun hears every line a person ran, nil when nobody is listening.
	OnRun func(line string)
}

// New makes a shell standing in the repository.
func New() *Shell {
	return &Shell{cwd: Repo}
}

// Greet returns the first prompt. A pane starts at a bare prompt: the page
// explains the tour, so the shell does not print a billboard every time a pane
// opens, and a second window is not a second greeting.
func (s *Shell) Greet() []byte {
	return []byte(s.prompt())
}

// Repaint returns the whole screen again, drawn to fit rows lines. A pane calls
// it after a resize: the bytes on screen cannot be rewrapped, but the lines
// behind them can be drawn again.
func (s *Shell) Repaint(rows int) []byte {
	keep := rows - 1
	if keep < 1 {
		keep = 1
	}
	start := len(s.scroll) - keep
	if start < 0 {
		start = 0
	}
	out := "\x1b[2J\x1b[H"
	for _, line := range s.scroll[start:] {
		out += line + "\r\n"
	}
	return []byte(out + s.prompt() + string(s.line))
}

// record keeps the lines of a print, so Repaint can draw them again.
func (s *Shell) record(out string) {
	for _, line := range strings.Split(strings.TrimSuffix(out, "\r\n"), "\r\n") {
		s.scroll = append(s.scroll, line)
	}
}

// Cwd is where the shell stands.
func (s *Shell) Cwd() string { return s.cwd }

// prompt is the prompt line for the current directory.
func (s *Shell) prompt() string {
	dir := s.cwd
	if dir == Home {
		dir = "~"
	} else if strings.HasPrefix(dir, Home+"/") {
		dir = "~" + strings.TrimPrefix(dir, Home)
	}
	return sprintf(prompt, dir)
}

// Feed runs one read of keys and returns what the terminal should show.
func (s *Shell) Feed(in []byte) []byte {
	var out []byte
	for _, r := range []rune(string(in)) {
		switch {
		case s.esc == 1:
			// ESC then a byte: a two-byte sequence, and '[' or 'O' starts a
			// longer one.
			if r == '[' || r == 'O' {
				s.esc = 2
			} else {
				s.esc = 0
			}
		case s.esc == 2:
			// A CSI ends at its first final byte. Up and down walk the
			// history; every other sequence is swallowed.
			if r >= 0x40 && r <= 0x7e {
				s.esc = 0
				switch r {
				case 'A':
					out = append(out, s.up()...)
				case 'B':
					out = append(out, s.down()...)
				}
			}
		case r == 0x1b:
			s.esc = 1
		case r == '\r':
			out = append(out, "\r\n"...)
			s.remember(string(s.line))
			s.scroll = append(s.scroll, s.prompt()+string(s.line))
			answer := string(s.run(string(s.line)))
			s.record(answer)
			out = append(out, answer...)
			s.line = s.line[:0]
			out = append(out, s.prompt()...)
		case r == 0x7f || r == 0x08:
			if n := len(s.line); n > 0 {
				s.line = s.line[:n-1]
				out = append(out, "\b \b"...)
			}
		case r == 0x03:
			s.line = s.line[:0]
			out = append(out, "^C\r\n"...)
			out = append(out, s.prompt()...)
		case r == 0x04:
			// The pane outlives the shell, so ctrl+d prints what a shell says
			// and hands back a prompt instead of ending anything.
			s.line = s.line[:0]
			out = append(out, "exit\r\n"...)
			out = append(out, s.prompt()...)
		case r == 0x0c:
			out = append(out, "\x1b[2J\x1b[H"...)
			out = append(out, s.prompt()...)
			out = append(out, string(s.line)...)
		case r == 0x15:
			for range s.line {
				out = append(out, "\b \b"...)
			}
			s.line = s.line[:0]
		case r < 0x20:
			// Every other control key does nothing, as at a live prompt.
		default:
			s.line = append(s.line, r)
			out = append(out, string(r)...)
		}
	}
	return out
}

// remember keeps a line so up can find it again. A repeat of the last line is
// not stored twice, and a blank line is not stored at all.
func (s *Shell) remember(line string) {
	if line != "" && (len(s.history) == 0 || s.history[len(s.history)-1] != line) {
		s.history = append(s.history, line)
	}
	s.at = len(s.history)
	s.draft = nil
}

// up walks back through the history, keeping the line being typed so down can
// return to it.
func (s *Shell) up() []byte {
	if s.at == 0 {
		return nil
	}
	if s.at == len(s.history) {
		s.draft = append([]rune(nil), s.line...)
	}
	s.at--
	s.line = []rune(s.history[s.at])
	return s.redraw()
}

// down walks forward again, and lands on the line being typed after the newest
// entry.
func (s *Shell) down() []byte {
	if s.at >= len(s.history) {
		return nil
	}
	s.at++
	if s.at == len(s.history) {
		s.line = s.draft
	} else {
		s.line = []rune(s.history[s.at])
	}
	return s.redraw()
}

// redraw puts the prompt and the current line back on the row the cursor is
// on.
func (s *Shell) redraw() []byte {
	return []byte("\r\x1b[K" + s.prompt() + string(s.line))
}

// run answers one line.
func (s *Shell) run(line string) []byte {
	if s.OnRun != nil {
		s.OnRun(line)
	}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return nil
	}
	args := fields[1:]
	switch fields[0] {
	case "help":
		return []byte(lines(
			"echo WORDS   print the words back",
			"ls           list the folder",
			"cat FILE     print a file",
			"cd DIR       change folder",
			"pwd          print the folder",
			"git status   the repository's state",
			"clear        clear the screen",
		))
	case "echo":
		return []byte(strings.Join(args, " ") + "\r\n")
	case "pwd":
		return []byte(s.cwd + "\r\n")
	case "ls":
		return s.ls()
	case "cat":
		return s.cat(args)
	case "cd":
		return s.cd(args)
	case "git":
		return s.git(args)
	case "clear":
		s.scroll = nil
		return []byte("\x1b[2J\x1b[H")
	case "exit", "logout":
		return []byte("exit\r\n")
	default:
		return []byte(fields[0] + ": command not found\r\n")
	}
}

// ls prints the folder's entries, one per line, as ls -1 does.
func (s *Shell) ls() []byte {
	names := tree[s.cwd]
	if len(names) == 0 {
		return nil
	}
	return []byte(lines(names...))
}

// cat prints a file, or says why it cannot.
func (s *Shell) cat(args []string) []byte {
	if len(args) == 0 {
		return []byte("usage: cat FILE\r\n")
	}
	p := s.resolve(args[0])
	body, ok := files[p]
	if !ok {
		if _, isDir := tree[p]; isDir {
			return []byte("cat: " + args[0] + ": Is a directory\r\n")
		}
		return []byte("cat: " + args[0] + ": No such file or directory\r\n")
	}
	return []byte(strings.ReplaceAll(body, "\n", "\r\n"))
}

// cd moves the shell, refusing what is not a folder.
func (s *Shell) cd(args []string) []byte {
	to := Home
	if len(args) > 0 {
		to = args[0]
	}
	p := s.resolve(to)
	if _, isDir := tree[p]; !isDir {
		return []byte("cd: no such file or directory: " + to + "\r\n")
	}
	s.cwd = p
	return nil
}

// git answers the three questions the tour asks, and refuses the rest.
func (s *Shell) git(args []string) []byte {
	if !strings.HasPrefix(s.cwd+"/", Repo+"/") && s.cwd != Repo {
		return []byte("fatal: not a git repository (or any of the parent directories): .git\r\n")
	}
	if len(args) == 0 {
		return []byte("usage: git status | git log | git diff\r\n")
	}
	switch args[0] {
	case "status":
		return []byte(gitStatus)
	case "log":
		return []byte(gitLog)
	case "diff":
		return []byte(gitDiff)
	}
	return []byte("git: '" + args[0] + "' is not part of this pretend repository\r\n")
}

// resolve turns a path as typed into a clean absolute path.
func (s *Shell) resolve(p string) string {
	switch {
	case p == "~" || strings.HasPrefix(p, "~/"):
		p = Home + strings.TrimPrefix(p, "~")
	case !strings.HasPrefix(p, "/"):
		p = s.cwd + "/" + p
	}
	return path.Clean(p)
}

// lines joins lines with a carriage return before each newline, which is what
// a terminal at a cooked prompt needs.
func lines(ls ...string) string {
	var b strings.Builder
	for _, l := range ls {
		b.WriteString(l)
		b.WriteString("\r\n")
	}
	return b.String()
}

// sprintf is a tiny stand-in for fmt, whose only job here is one %s.
func sprintf(format, arg string) string {
	return strings.Replace(format, "%s", arg, 1)
}
