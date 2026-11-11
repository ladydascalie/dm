package kdl

import (
	"reflect"
	"testing"
)

func scanTokens(t *testing.T, src string) []Token {
	t.Helper()
	toks, err := Scan(src)
	if err != nil {
		t.Fatalf("Scan(%q) unexpected error: %v", src, err)
	}
	return toks
}

func TestScanEmptyAndBlank(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []Token
	}{
		{"empty", "", []Token{{Kind: EOF, Line: 1}}},
		{"blank lines", "\n\n\n", []Token{{Kind: EOF, Line: 4}}},
		{"whitespace only", " \t \r\n  \t", []Token{{Kind: EOF, Line: 2}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scanTokens(t, tt.src)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Scan(%q) = %v, want %v", tt.src, got, tt.want)
			}
		})
	}
}

func TestScanIdents(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []Token
	}{
		{"single ident", "palette", []Token{
			{Kind: Ident, Value: "palette", Line: 1},
			{Kind: EOF, Line: 1},
		}},
		{"idents on one line", "accents  {\n}", []Token{
			{Kind: Ident, Value: "accents", Line: 1},
			{Kind: LBrace, Value: "{", Line: 1},
			{Kind: RBrace, Value: "}", Line: 2},
			{Kind: EOF, Line: 2},
		}},
		{"ident stops at brace", "glyphs{", []Token{
			{Kind: Ident, Value: "glyphs", Line: 1},
			{Kind: LBrace, Value: "{", Line: 1},
			{Kind: EOF, Line: 1},
		}},
		{"ident stops at quote", "states\"x\"", []Token{
			{Kind: Ident, Value: "states", Line: 1},
			{Kind: String, Value: "x", Line: 1},
			{Kind: EOF, Line: 1},
		}},
		{"ident keeps punctuation", "#101014,-", []Token{
			{Kind: Ident, Value: "#101014,-", Line: 1},
			{Kind: EOF, Line: 1},
		}},
		{"line numbers across lines", "a\nbb {\nccc\n}", []Token{
			{Kind: Ident, Value: "a", Line: 1},
			{Kind: Ident, Value: "bb", Line: 2},
			{Kind: LBrace, Value: "{", Line: 2},
			{Kind: Ident, Value: "ccc", Line: 3},
			{Kind: RBrace, Value: "}", Line: 4},
			{Kind: EOF, Line: 4},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scanTokens(t, tt.src)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Scan(%q) = %v, want %v", tt.src, got, tt.want)
			}
		})
	}
}

func TestScanStrings(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{"plain", `"rail-bg"`, "rail-bg"},
		{"empty", `""`, ""},
		{"escape n t r", `"a\nb\tc\rd"`, "a\nb\tc\rd"},
		{"escaped quote and backslash", `"e\"f\\g"`, `e"f\g`},
		{"unknown escape keeps char", `"a\qb"`, "aqb"},
		{"inner braces are literal", `"{}"`, "{}"},
		{"theme colour value", `"#f0b84a"`, "#f0b84a"},
		{"glyph unicode", `"●"`, "●"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scanTokens(t, tt.src)
			want := []Token{{Kind: String, Value: tt.want, Line: 1}, {Kind: EOF, Line: 1}}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("Scan(%q) = %v, want %v", tt.src, got, want)
			}
		})
	}
}

func TestScanStringLines(t *testing.T) {
	// A literal newline inside a string is kept in the value and counted.
	src := "states {\n\"a\nb\" x\n}"
	got := scanTokens(t, src)
	want := []Token{
		{Kind: Ident, Value: "states", Line: 1},
		{Kind: LBrace, Value: "{", Line: 1},
		{Kind: String, Value: "a\nb", Line: 2},
		{Kind: Ident, Value: "x", Line: 3},
		{Kind: RBrace, Value: "}", Line: 4},
		{Kind: EOF, Line: 4},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Scan multiline string = %v, want %v", got, want)
	}
}

func TestScanLineComments(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []Token
	}{
		{"comment line then code", "// dark theme\npalette", []Token{
			{Kind: Ident, Value: "palette", Line: 2},
			{Kind: EOF, Line: 2},
		}},
		{"comment swallows rest of line", "a// c { \"x\"\nb", []Token{
			{Kind: Ident, Value: "a", Line: 1},
			{Kind: Ident, Value: "b", Line: 2},
			{Kind: EOF, Line: 2},
		}},
		{"comment at end without newline", "a// tail", []Token{
			{Kind: Ident, Value: "a", Line: 1},
			{Kind: EOF, Line: 1},
		}},
		{"comment only file", "// nothing here", []Token{
			{Kind: EOF, Line: 1},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scanTokens(t, tt.src)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Scan(%q) = %v, want %v", tt.src, got, tt.want)
			}
		})
	}
}

func TestScanBlockComments(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []Token
	}{
		{"single line", "a/* c */b", []Token{
			{Kind: Ident, Value: "a", Line: 1},
			{Kind: Ident, Value: "b", Line: 1},
			{Kind: EOF, Line: 1},
		}},
		{"multiline counts lines", "a\n/* x\ny\nz */\nb", []Token{
			{Kind: Ident, Value: "a", Line: 1},
			{Kind: Ident, Value: "b", Line: 5},
			{Kind: EOF, Line: 5},
		}},
		{"star slash inside needs no space", "/*x*/a", []Token{
			{Kind: Ident, Value: "a", Line: 1},
			{Kind: EOF, Line: 1},
		}},
		{"block comment at end without newline", "a/* tail */", []Token{
			{Kind: Ident, Value: "a", Line: 1},
			{Kind: EOF, Line: 1},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := scanTokens(t, tt.src)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Scan(%q) = %v, want %v", tt.src, got, tt.want)
			}
		})
	}
}

func TestScanBlockCommentUnterminated(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string
	}{
		{"single line", "a /* oops", "line 1: unterminated block comment"},
		{"start line reported", "\n\n/* x\ny", "line 3: unterminated block comment"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toks, err := Scan(tt.src)
			if err == nil || err.Error() != tt.wantErr {
				t.Errorf("Scan(%q) error = %v, want %q", tt.src, err, tt.wantErr)
			}
			if toks != nil {
				t.Errorf("Scan(%q) on error should return nil tokens, got %v", tt.src, toks)
			}
		})
	}
}

func TestScanStringUnterminated(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string
	}{
		{"simple", "a \"oops", "line 1: unterminated string"},
		{"start line reported", "\n\"x\ny", "line 2: unterminated string"},
		{"trailing backslash at end of input", `"ab\`, "line 1: unterminated string"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toks, err := Scan(tt.src)
			if err == nil || err.Error() != tt.wantErr {
				t.Errorf("Scan(%q) error = %v, want %q", tt.src, err, tt.wantErr)
			}
			if toks != nil {
				t.Errorf("Scan(%q) on error should return nil tokens, got %v", tt.src, toks)
			}
		})
	}
}

func TestScanNestedDocument(t *testing.T) {
	// A miniature of the real theme shape: nested blocks, quoted keys and
	// values, comments, blank lines.
	src := `// theme: mini
palette {
  "rail-bg" "#101014"
}

states {
  "working" { "glyph" "●" "colour" "#f0b84a" } // accent
}
`
	got := scanTokens(t, src)
	var kinds []Kind
	var strs []string
	for _, tok := range got {
		kinds = append(kinds, tok.Kind)
		if tok.Kind == String {
			strs = append(strs, tok.Value)
		}
	}
	wantKinds := []Kind{
		Ident, LBrace, String, String, RBrace,
		Ident, LBrace, String, LBrace, String, String, String, String, RBrace, RBrace,
		EOF,
	}
	if !reflect.DeepEqual(kinds, wantKinds) {
		t.Errorf("kind sequence = %v, want %v", kinds, wantKinds)
	}
	wantStrs := []string{"rail-bg", "#101014", "working", "glyph", "●", "colour", "#f0b84a"}
	if !reflect.DeepEqual(strs, wantStrs) {
		t.Errorf("strings = %q, want %q", strs, wantStrs)
	}
	// Every token line: brace structure and comments land where expected.
	last := got[len(got)-1]
	if last.Kind != EOF || last.Line != 9 {
		t.Errorf("EOF token = %+v, want line 9", last)
	}
	if got[14].Line != 8 { // closing brace of states, before the trailing blank line
		t.Errorf("closing brace line = %d, want 8", got[14].Line)
	}
}
