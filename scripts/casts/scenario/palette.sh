cols=132
rows=34
seconds=16

prepare() {
	demo dm exec '{"action":"split","target":"w1p1","dir":"right"}' -s "$session" >/dev/null
	demo dm write-to w1p1 --msg 'dm list' --enter -s "$session" >/dev/null
	sleep 1
}

feed() {
	sleep 1.8
	key '\x02' 1.6  # the prefix box names K
	key 'K' 1.8     # the command palette over the panes
	say 'layout' 1.2
	key '?' 2.0     # the selection explained
	key '\r' 2.2    # run it: the layout palette opens
	key 'j' 1.8     # the preview follows the selection
	key 'j' 1.8
}
