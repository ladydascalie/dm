#!/usr/bin/env bash
# record.sh records the demo casts the docs site plays.
#
#   scripts/casts/record.sh             record every demo in scripts/casts/scenario
#   scripts/casts/record.sh split       record one
#   scripts/casts/record.sh --dry-run   print each demo's screen and length
#
# A cast is generated, so the repo keeps the recipe. A UI change re-records the
# cast instead of leaving a stale lie on the page. Watch one with
# `uvx asciinema play docs/casts/split.cast`.
#
# One demo is one file. scripts/casts/scenario/<name>.sh sets cols, rows and
# seconds and defines feed(), which writes the keystrokes. Everything the demos
# share lives here: the scratch environment, the recorder flags and the check
# every fresh cast must pass.
#
# The run is hermetic. A scratch DM_STATE and XDG_CONFIG_HOME mean the
# operator's sessions, config and theme never reach a cast, and the `dm ls`
# inside a demo shows the demo alone. SHELL points at a three-line wrapper that
# runs zsh against a scratch ZDOTDIR, so a cast shows a plain prompt instead of
# a local theme. dm reads SHELL to pick a pane's shell, and the wrapper keeps
# the path out of the recorded screen.
#
# Each demo owns ${TMPDIR:-/tmp}/dm-casts/<name> and a session named
# demo-<name>, so two recorders never share a state directory or a socket.
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

# The cast paths are repo paths, so the script works from anywhere.
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$root"

scenarios=scripts/casts/scenario
casts=docs/casts
work=${TMPDIR:-/tmp}/dm-casts

if command -v asciinema >/dev/null 2>&1; then
	recorder=(asciinema)
else
	recorder=(uvx asciinema)
fi

# The three helpers a scenario file may call. A scenario writes to stdout, so
# the feeder's timing is the only source of a cast's rhythm.

# key sends the bytes in $1, escape sequences included, then waits.
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

# run types a command and presses enter.
run() {
	say "$1" 0.2
	key '\r' "${2:-1.2}"
}

scenario_names() {
	local file
	for file in "$scenarios"/*.sh; do
		[ -f "$file" ] || continue
		file=${file##*/}
		printf '%s\n' "${file%.sh}"
	done
}

# load sources one scenario and fails unless it states the screen and the
# length and defines the feeder. The dry run and the recording read the same
# three variables from the same file, so they cannot disagree.
load() {
	local name=$1 file=$scenarios/$1.sh v
	if [ ! -f "$file" ]; then
		printf 'record: no scenario %s\n' "$file" >&2
		return 1
	fi
	unset -f feed prepare
	cols= rows= seconds=
	# shellcheck source=/dev/null
	. "$file"
	for v in cols rows seconds; do
		case ${!v:-} in
		'')
			printf 'record: %s sets no %s\n' "$file" "$v" >&2
			return 1
			;;
		*[!0-9]*)
			printf 'record: %s sets a non-numeric %s: %s\n' "$file" "$v" "${!v}" >&2
			return 1
			;;
		esac
	done
	if ! declare -F feed >/dev/null; then
		printf 'record: %s defines no feed()\n' "$file" >&2
		return 1
	fi
}

# setup points the shared steps at one demo's scratch directory, session and
# cast path.
setup() {
	name=$1
	dir=$work/$name
	state=$dir/state
	conf=$dir/config
	zdot=$dir/zsh
	cwd=$dir/api
	session=demo-$name
	out=$casts/$name.cast
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
	rm -rf "$dir"
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
	local name=$1 events last
	setup "$name"
	prep
	start
	# The optional prepare hook runs before the recorder attaches, so a demo can
	# arrange its panes off camera and spend its seconds on the keys it shows.
	if declare -F prepare >/dev/null; then
		prepare
	fi
	feed | demo timeout -k 5 $((seconds * 2 + 20)) "${recorder[@]}" rec \
		--stdin --overwrite --quiet \
		--idle-time-limit 2 \
		--cols "$cols" --rows "$rows" \
		--title "dm $name" \
		-c "timeout -s KILL $seconds dm attach -s $session" \
		"$out"
	demo dm kill -s "$session" >/dev/null 2>&1 || true
	demo dm purge >/dev/null 2>&1 || true
	events=$(grep -c '^\[' "$out" || true)
	last=$(grep -o '^\[[0-9.]*' "$out" | tail -1 | tr -d '[')
	printf 'cast: %s: %s events, ends at %ss, %s bytes\n' "$name" "$events" "$last" "$(wc -c <"$out")"
	"$root/scripts/casts/check.sh" "$out"
}

dry_run=0
if [ "${1:-}" = --dry-run ]; then
	dry_run=1
	shift
fi

if [ $# -eq 0 ]; then
	mapfile -t names < <(scenario_names)
else
	names=("$@")
fi
if [ ${#names[@]} -eq 0 ]; then
	echo "record: no scenario in $scenarios" >&2
	exit 1
fi

for name in "${names[@]}"; do
	load "$name"
	if [ "$dry_run" = 1 ]; then
		printf 'dry-run: %s: cols %s, rows %s, seconds %s, feed ok\n' "$name" "$cols" "$rows" "$seconds"
	else
		record "$name"
	fi
done
