# windows: a second window, the tab bar, and the preset layouts
#
# Sourced by scripts/casts/record.sh. cols, rows and seconds are required and
# feed() writes the keystrokes to stdout, through key, say and run.

cols=132
rows=34
seconds=16

# Window 1 carries output off camera, so the two windows read apart when the
# demo moves between them and the empty panes never look like the same screen.
prepare() {
	demo dm write-to w1p1 --msg 'echo window one' --enter -s "$session" >/dev/null
	sleep 0.4
}

feed() {
	sleep 1.4
	key '\x02' 1.4  # the prefix box lists every key
	key 'c' 1.4     # a second window, and the tab bar gains it
	key '\x02' 0.45
	key '1' 0.9     # window 1
	key '\x02' 0.45
	key '2' 0.9     # window 2
	key '\x02' 0.45
	key '|' 0.8     # split right: two panes
	key '\x02' 0.45
	key '-' 0.9     # split down: three panes
	key '\x02' 0.45
	key 'u' 0.9     # even horizontal
	key '\x02' 0.45
	key 'u' 0.9     # even vertical
	key '\x02' 0.45
	key 'u' 0.8     # main horizontal
	key '\r' 0.9    # a prompt row under the layout, so the cast ends on it
	sleep 0.6
}
