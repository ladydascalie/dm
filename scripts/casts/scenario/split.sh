# split: the prefix box, a split right, then a split down.
#
# Sourced by scripts/casts/record.sh. cols, rows and seconds are required and
# feed() writes the keystrokes to stdout, through key, say and run.

cols=132
rows=34
seconds=16

feed() {
	sleep 1.8
	key '\x02' 2.0  # the prefix box lists every key
	key '|' 1.4     # split right
	key '\x02' 1.0
	key 'h' 1.0     # focus left
	key '\x02' 1.0
	key '-' 1.6     # split down
	sleep 2.0
}
