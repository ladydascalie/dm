#!/usr/bin/env bash
# record.sh records the demo casts the docs site plays.
#
#   scripts/casts/record.sh            record every cast
#   scripts/casts/record.sh split      record one
#
# A cast is generated, so the repo keeps the recipe. A UI change re-records the
# cast instead of leaving a stale lie on the page. Watch one with
# `uvx asciinema play docs/casts/split.cast`.
#
# The run is hermetic. A scratch DM_STATE and XDG_CONFIG_HOME mean the
# operator's sessions, config and theme never reach a cast, and the `dm ls`
# inside a demo shows the demo alone. SHELL points at a three-line wrapper that
# runs zsh against a scratch ZDOTDIR, so a cast shows a plain prompt instead of
# a local theme. dm reads SHELL to pick a pane's shell, and the wrapper keeps
# the path out of the recorded screen.
#
# asciinema drives the session from a feeder piped into --stdin, so no key here
# is typed by a hand and the timing is the file. Three traps:
#   - asciinema exits when its stdin closes and its child exits, so the feeder
#     and the command time out together.
#   - dm refuses to attach from inside a dm pane. The recorder drops DM_SESSION
#     and DM_PANE, because asciinema's pty is a real terminal boundary.
#   - a cast must end on the finished screen, so the attach is killed rather
#     than detached, which would record the restore.
set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"

work=${TMPDIR:-/tmp}/dm-casts
state=$work/state
conf=$work/config
zdot=$work/zsh
session=demo
cwd=${TMPDIR:-/tmp}/api
casts=docs/casts

if command -v asciinema >/dev/null 2>&1; then
	recorder=(asciinema)
else
	recorder=(uvx asciinema)
fi

key() {
	printf '%b' "$1"
	sleep "${2:-0.5}"
}

# say types a command a character at a time, so the cast reads as a hand.
say() {
	local s=$1 i
	for ((i = 0; i < ${#s}; i++)); do
		printf '%s' "${s:i:1}"
		sleep 0.045
	done
	sleep "${2:-0.3}"
}

run() {
	say "$1" 0.2
	key '\r' "${2:-1.2}"
}

feed_split() {
	sleep 1.8
	key '\x02' 2.0  # the prefix box lists every key
	key '|' 1.4     # split right
	key '\x02' 1.0
	key 'h' 1.0     # focus left
	key '\x02' 1.0
	key '-' 1.6     # split down
	sleep 2.0
}

feed_project() {
	sleep 1.8
	key '\x02' 1.6
	key 'P' 1.8     # the project prompt
	say 'go-backend' 0.5
	key '\r' 2.4    # the rail regroups
	run 'dm ls' 2.6 # the project outlives the session
	key '\r' 1.4   # a prompt row under the table, so the cast ends on it
}

# demo runs one command under the demo environment: scratch state and config,
# no update check, and the wrapper shell.
demo() {
	env -u DM_SESSION -u DM_PANE \
		"DM_STATE=$state" \
		"XDG_CONFIG_HOME=$conf" \
		"SHELL=$zdot/shell" \
		DM_NO_UPDATE_CHECK=1 \
		"$@"
}

prep() {
	rm -rf "$work"
	mkdir -p "$state" "$conf" "$zdot" "$cwd"
	cat >"$zdot/.zshrc" <<'ZSH'
PS1='$ '
PROMPT_EOL_MARK=''
HISTFILE=/dev/null
setopt no_beep
precmd() { print -Pn '\e]133;D\e\\\e]133;A\e\\' }; preexec() { print -Pn '\e]133;C\e\\' }
ZSH
	cat >"$zdot/shell" <<SHELL
#!/bin/sh
exec env ZDOTDIR=$zdot zsh
SHELL
	chmod +x "$zdot/shell"
	mkdir -p "$casts"
}

start() {
	demo dm reset -s "$session" --force >/dev/null 2>&1 || true
	(
		cd "$cwd" || exit 1
		demo dm new -s "$session" >/dev/null
	)
	# A thinner rail than the default 34 leaves the panes more room and still
	# shows the tree, the project group and the pane state.
	demo dm exec '{"action":"set-rail","index":17}' -s "$session" >/dev/null
	# The client draws before the feeder types, and a session that never comes
	# up records toasts instead of a demo, so this fails instead of shipping one.
	for _ in $(seq 60); do
		demo dm list -s "$session" 2>/dev/null | grep -q '^w1p1' && return 0
		sleep 0.1
	done
	echo "record: session $session did not come up" >&2
	return 1
}

record() {
	local name=$1 len cols rows out=$casts/$1.cast events last
	# One screen size for both casts. The player scales the font to the column,
	# so a wider screen buys room for the help box and the dm ls table and pays
	# for it in glyph size. The box stops shortening labels at 120 columns; 132
	# leaves it clear of the rail, the pane borders and the status bar.
	cols=132
	rows=34
	case $name in
	split) len=16 ;;
	project) len=17 ;;
	esac
	start
	"feed_$name" | demo timeout -k 5 $((len * 2 + 20)) "${recorder[@]}" rec \
		--stdin --overwrite --quiet \
		--idle-time-limit 2 \
		--cols "$cols" --rows "$rows" \
		--title "dm $name" \
		-c "timeout -s KILL $len dm attach -s $session" \
		"$out"
	demo dm kill -s "$session" >/dev/null 2>&1 || true
	events=$(grep -c '^\[' "$out" || true)
	last=$(grep -o '^\[[0-9.]*' "$out" | tail -1 | tr -d '[')
	printf 'cast: %s: %s events, ends at %ss, %s bytes\n' "$name" "$events" "$last" "$(wc -c <"$out")"
	gaps=$(font_gaps "$out") || {
		printf 'record: %s did not parse as a cast\n' "$name" >&2
		exit 1
	}
	if [ -n "$gaps" ]; then
		printf 'record: %s draws %s, which the webfont lacks. Run make docs-font.\n' "$name" "$gaps" >&2
		exit 1
	fi
}

# A cast carries codepoints, and the webfont carries the shapes. A codepoint the
# subset lacks is tofu on the page, so a fresh cast is checked against the font
# that ships beside it.
font_gaps() {
	uvx --quiet --with fonttools python - "$1" docs/assets/font/dm-cast-mono.woff2 <<'PY'
import json, sys
from fontTools.ttLib import TTFont

cast, font = sys.argv[1], sys.argv[2]
have = set(TTFont(font).getBestCmap())
codes = set()
for line in open(cast, encoding='utf-8', errors='ignore'):
	line = line.strip()
	if not line.startswith('['):
		continue
	row = json.loads(line)
	if row[1] == 'o':
		codes.update(ord(ch) for ch in row[2])
missing = sorted(c for c in codes if c > 0x7f and c not in have)
print(' '.join('U+%04X' % c for c in missing))
PY
}

names=("$@")
if [ ${#names[@]} -eq 0 ]; then
	names=(split project)
fi

prep
for name in "${names[@]}"; do
	record "$name"
done
demo dm purge >/dev/null 2>&1 || true
