# scroll: scrollback, search and a copy.
#
# Sourced by scripts/casts/record.sh. cols, rows and seconds are required and
# feed() writes the keystrokes to stdout, through key, say and run.
#
# seq fills the pane past its height, so there is history to reach. `[` opens
# scrollback and the bar names the offset, `k` walks up a row at a time, `/`
# searches a term that appears once near the top of the buffer, `enter` jumps
# the view to it, `n` returns after a `k` leaves it, `g` jumps to the top of the
# buffer, and the pair of `esc` clears the search and then leaves. `prefix y`
# copies what the pane produced since the last enter, and the bar toasts the
# character count.

cols=132
rows=34
seconds=16

feed() {
	sleep 1.6
	run "seq -f 'line %02g' 1 48" 1.8  # more lines than the pane holds
	key '\x02' 0.8                     # the prefix box
	key '[' 1.2                        # scrollback: the bar names the offset
	key 'k' 0.4
	key 'k' 0.4                        # two rows up
	key '/' 0.5                        # the query row
	say 'line 03' 0.3                  # one match, near the top of the buffer
	key '\r' 1.0                       # accept: the match lands at the top
	key 'k' 0.6                        # move off it
	key 'n' 0.9                        # and the match comes back
	key 'g' 0.7                        # the top of the buffer
	key '\x1b' 0.5                     # clear the search
	key '\x1b' 0.7                     # leave scrollback
	key '\x02' 0.5
	key 'y' 1.3                        # copy the pane's output, then the toast
	sleep 1.0
}
