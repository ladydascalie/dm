# shot testdata

Data the golden harness reads. No Go code here.

## themes/

Three KDL theme files, loaded with `theme.LoadThemeFile("internal/shot/testdata", name)`:

- `dark` - low-glare dark rail.
- `light` - paper rail, dark text. A real light theme, not a flipped dark one.
- `high-contrast` - black rail, white text, saturated accents.

Each file sets all four blocks (`palette`, `accents`, `glyphs`, `states`) so the
parser is exercised. Keys are the ones `internal/theme/theme.go` accepts.

## scenes.json

Top level is `{ "scenes": [ ... ] }`. Each scene is flat:

| field | type | meaning |
|---|---|---|
| `name` | string | scene id, also the golden file stem |
| `cols`, `rows` | int | terminal size |
| `rail` | int | rail width in cells; 0 hides it |
| `theme` | string | theme file base name in `themes/` |
| `root` | object | pane tree: `{"pane":"id"}` or `{"dir":"row"\|"col","kids":[...]}` |
| `panes` | array | `{id, name, content}`; `content` is written to the pane's VT |
| `windows` | array | `{id, name}` for the tab bar |
| `activeWindow` | int | index into `windows` |
| `focus` | string | focused pane id |
| `toast` | string | optional toast text |
| `float` | object | optional `{id, x, y, w, h, content}` floating pane |
| `switcher` | bool | optional; open the switcher |
| `help` | bool | optional; open the help box |
| `mode` | string | optional; `"sessions"` opens the session list |
| `sessions` | array | optional `{name, active}` for the session list |

`content` is a JSON string. `\n` is a newline and `\u001b` is ESC, so a pane can
carry SGR attributes.

### Scenes

- `split` - two panes in a row, rail, tab bar, bar, toast.
- `float` - split plus a floating pane with its own VT.
- `switcher` - split plus the switcher palette.
- `help` - split plus the help box.
- `sessions` - split plus the session list.
- `cjk` - one pane with the exact bytes from the ui-fidelity tasks file: three
  CJK characters, a three-person ZWJ family emoji, a combining acute, and a line
  with italic, faint, strikethrough and a curly underline. This is the width
  invariant's fixture.
