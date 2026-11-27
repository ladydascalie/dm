package clidoc

// The copy surface is the mouse selection, the prefix yank keys and the
// clipboard they write to. The man page, the HTML page and llms.txt render
// these strings, and none of them invents its own wording.

// CopyDrag says what a mouse drag does and where the selection stops.
const CopyDrag = "dm turns mouse reporting on at attach. Every drag on a pane then reaches dm, and the terminal never sees it. A left press on pane content starts a selection. The drag highlights a row-major range. The release copies it and raises a copied N chars toast. The range stops at the pane the drag started in. A press and release in the same cell stays a click. An app that claims the mouse gets that click. A wheel turn during the drag scrolls that pane's scrollback three rows a notch. A pane on the alternate screen keeps no history, and dm says so in a toast."

// CopyRows says what text the range yields.
const CopyRows = "The copy is the rows the pane drew, not a logical line. A wrapped line arrives in pieces, one per row. A two-column cell arrives once. Trailing spaces are dropped."

// CopyDrop says when a selection goes away.
const CopyDrop = "A geometry change drops the selection. So does output that scrolls the live screen. A selection in frozen scrollback holds until the pane leaves scrollback."

// CopyTerminal names the modifier that hands a drag back to the terminal.
const CopyTerminal = "dm claims the drag first, so the terminal's own selection needs its bypass modifier. Hold Shift on most terminals and Option on iTerm2. When neither works, the terminal's own mouse preference names the key."

// CopyClipboard says how a copy reaches the clipboard.
const CopyClipboard = "A copy writes OSC 52. dm probes the terminal once per attach with an OSC 52 read. A terminal silent for 300 ms gets the copy piped to pbcopy, wl-copy or xclip instead. The toast says copied only when the text landed. When the host carries no clipboard tool, the toast names one to install."

// CopyPaste says what a paste does in a pane.
const CopyPaste = "dm enables bracketed paste on attach. A paste inserts newlines literally, and no byte inside it is read as a dm key."

// CopyIterm2 is what iTerm2 needs before a copy and a paste behave as above.
const CopyIterm2 = "iTerm2 drops OSC 52 writes until Preferences → General → Selection → Applications in terminal may access clipboard is on. A dropped write looks the same as a taken one, so turn the setting on before you depend on a copy there. When dm's read probe goes unanswered, dm pipes the copy to pbcopy instead, and that copy lands. iTerm2 pastes with Command+V, and dm reads it as one bracketed paste."
