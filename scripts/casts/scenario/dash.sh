# dash: the dashboard as a table of the whole session.
#
# Sourced by scripts/casts/record.sh. cols, rows and seconds are required and
# feed() writes the keystrokes to stdout, through key, say and run.

cols=132
rows=34
seconds=16

# task prints one pane's agent status, window title and a few lines of work, so
# the dashboard has rows worth reading.
task() {
	cat <<'TASK'
#!/bin/sh
# One demo pane: status, title, then the output of the job it shows.
case $1 in
api)
	printf '\033]7501;state=working:app=pi\007'
	title='go test ./internal/auth'
	printf 'ok   internal/auth 0.41s\n'
	printf 'auth: no test files\n'
	;;
web)
	printf '\033]7501;state=blocked:app=pi\007'
	title='vite dev'
	printf 'vite v6 ready 214ms\n'
	printf 'Local http://:5173\n'
	printf 'press h + enter\n'
	;;
docs)
	printf '\033]7501;state=done:app=pi\007'
	title='make docs'
	printf 'mkdocs build\n'
	printf 'building docs...\n'
	printf 'built in 2.31s\n'
	;;
review)
	printf '\033]7501;state=idle:app=claude\007'
	title='hg status'
	printf 'M docs/demos.md\n'
	printf 'M README.md\n'
	printf '?? casts/scenario/dash.sh\n'
	;;
lint)
	printf '\033]7501;state=error:app=pi\007'
	title='golangci-lint run'
	printf 'handler.go:42:2 errcheck\n'
	printf 'w.Write unchecked\n'
	printf '1 issues\n'
	;;
esac
printf '\033]0;%s\007' "$title"
TASK
}

prepare() {
	# Two checkouts, so the git cell reads a real tree for every pane.
	git -C "$cwd" init -q -b main
	printf 'package auth\n' >"$cwd/auth.go"
	git -C "$cwd" -c user.name=demo -c user.email=demo@example.com add auth.go
	git -C "$cwd" -c user.name=demo -c user.email=demo@example.com commit -qm init
	mkdir -p "$dir/web" "$cwd/internal"
	git -C "$dir/web" init -q -b feat/vite
	printf 'export const ready = 1\n' >"$dir/web/main.js"
	git -C "$dir/web" -c user.name=demo -c user.email=demo@example.com add main.js
	git -C "$dir/web" -c user.name=demo -c user.email=demo@example.com commit -qm init

	task >"$dir/task"
	chmod +x "$dir/task"

	demo dm exec '{"action":"split","target":"w1p1","dir":"right"}' -s "$session" >/dev/null
	sleep 0.4
	demo dm exec '{"action":"split","target":"w1p2","dir":"right"}' -s "$session" >/dev/null
	sleep 0.4
	demo dm exec '{"action":"split","target":"w1p1","dir":"down"}' -s "$session" >/dev/null
	sleep 0.4
	demo dm exec '{"action":"split","target":"w1p2","dir":"down"}' -s "$session" >/dev/null
	sleep 1.2

	demo dm set-project s1 go-backend -s "$session" >/dev/null

	# Every pane gets its work off camera, so the feed spends its seconds on the
	# keys the demo shows. `clear` runs first, so the typed line leaves the
	# screen and the pane keeps only its output. The last pane to report is the
	# newest, and the dashboard opens on it.
	write() {
		local pane=$1 name=$2 job=$3 where=$4
		demo dm exec "{\"action\":\"set-pane-name\",\"target\":\"$pane\",\"name\":\"$name\"}" -s "$session" >/dev/null
		demo dm write-to "$pane" --msg "clear; cd '$where'; '$dir/task' $job" --enter -s "$session" >/dev/null
	}
	# Each pane's directory is a fresh one inside a checkout, so the git cell
	# reads a real tree, and a second apart so the time cell differs.
	write w1p3 docs docs "$cwd/internal"
	sleep 0.7
	write w1p4 review review "$cwd/internal"
	sleep 0.7
	write w1p2 web web "$dir/web"
	sleep 0.7
	write w1p1 auth-api api "$cwd/internal"
	sleep 0.7
	write w1p5 lint lint "$cwd/internal"
	sleep 1.0
	demo dm exec '{"action":"focus","target":"w1p1"}' -s "$session" >/dev/null
}

feed() {
	sleep 1.6       # the session, five panes with their work
	key '\x02' 0.7  # the prefix box
	key 'a' 1.8      # the dashboard
	key 'p' 1.2      # hide the preview band, so the table takes the screen
	key '/' 0.7      # filter mode
	say 'lint' 1.1   # one row keeps the word
	key '\x1b' 0.6   # clears the query
	key '\x1b' 0.9   # leaves filter mode
	key 's' 1.3      # order: project
	key 's' 1.5      # order: status, the states that want attention first
	key '2' 2.4      # row 2 is the lint pane: jump to it
}
