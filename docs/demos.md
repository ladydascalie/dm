# Demos

One recorded session per surface. A player opens on a still frame, so press play
once. Every key the recording sends lands in the bottom right of the terminal as
a chip, so the demo names the key and shows what it does. `k` turns that overlay
off and on.

`scripts/casts/record.sh` records every cast against a scratch state and config.

## Windows and layouts

<div data-cast="docs/casts/windows.cast" data-poster="npt:0:13"></div>

`ctrl+b c` opens a second window and the tab bar gains it. A digit selects a
window, and `ctrl+b u` reflows that window's three panes through the preset
layouts.

## Moving panes

<div data-cast="docs/casts/panes.cast" data-poster="npt:0:11"></div>

`ctrl+b z` zooms the focused pane, and the same key zooms it back. `ctrl+b f`
shelves it, `ctrl+b F` floats the shelf, and `ctrl+b G` pops the pane back into
the layout.

## Scrollback and copy

<div data-cast="docs/casts/scroll.cast" data-poster="npt:0:09"></div>

`ctrl+b [` opens the pane's scrollback, and `k`, `g` and `/` move through it
while the bar names the offset. `ctrl+b y` copies the pane's output since the
last enter, and the bar toasts the character count.

## The dashboard

<div data-cast="docs/casts/dash.cast" data-poster="npt:0:05"></div>

`ctrl+b a` opens the dashboard: every pane in the session, one row each, with
the project, the agent, the tree state and how long ago it last moved. `/`
filters, `s` reorders, and a digit jumps to that pane.

## The command palette

<div data-cast="docs/casts/palette.cast" data-poster="npt:0:07"></div>

`ctrl+b K` opens the command palette, and typing narrows every action to the few
that match. `?` explains the selected action, and `enter` runs it: the layout
palette opens over the panes.
