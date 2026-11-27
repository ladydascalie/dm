# panes: zoom a pane, shelve it, float the shelf, then pop it back.
#
# Sourced by scripts/casts/record.sh. cols, rows and seconds are required and
# feed() writes the keystrokes to stdout, through key, say and run.

cols=132
rows=34
seconds=15

# prepare builds the two panes off camera: a small git repo in the demo folder,
# a log in the left pane and a status in the right one, so the cast opens on
# two panes with content worth moving and the focus is on the right pane.
prepare() {
	demo dm exec '{"action":"split","target":"w1p1","dir":"right"}' -s "$session" >/dev/null
	demo sh -c '
		cd "$1" || exit 1
		git init -q -b main
		printf "package api\n\nfunc Healthz() string { return \"ok\" }\n" >main.go
		printf "test:\n\tgo test ./...\n" >Makefile
		printf "# api\n" >README.md
		git add .
		commit() { git -c user.name=demo -c user.email=demo@example.com commit -qm "$1"; }
		commit "api: add healthz handler"
		printf "func Split() {}\n" >>main.go; git add .; commit "api: split a pane by id"
		printf "func Shelf() {}\n" >>main.go; git add .; commit "api: shelve a pane"
		printf "## working tree\n" >>README.md
	' sh "$cwd" >/dev/null 2>&1
	demo dm write-to w1p1 --msg 'git log --oneline' --enter -s "$session" >/dev/null
	demo dm write-to w1p2 --msg 'git status -sb' --enter -s "$session" >/dev/null
	sleep 1
}

feed() {
	sleep 1.4       # two panes, a log in one and a status in the other
	key '\x02' 0.8  # the prefix box lists every key
	key 'z' 1.9     # zoom: the pane fills the window
	key '\x02' 0.8
	key 'z' 1.9     # zoom again: the split comes back
	key '\x02' 0.8
	key 'f' 1.9     # shelve: the pane leaves the grid, the rail shows the shelf
	key '\x02' 0.8
	key 'F' 1.9     # the shelf floats over the panes
	key '\x02' 0.8
	key 'G' 1.8     # pop: the shelf member returns to the layout
}
