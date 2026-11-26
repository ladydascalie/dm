#!/usr/bin/env bash
# release-notes.sh prints the markdown notes for one release.
#
#   scripts/release-notes.sh [prev-tag] [tag]
#
# prev-tag is the release the new one follows. A missing prev-tag means "the
# most recent tag reachable from tag", and a repository with no tag at all
# means "every commit". A missing tag means the most recent tag, else HEAD.
#
# The output is "## New in <tag>" and then the groups that hold a commit, in
# this order: Features, Fixes, Performance, Docs, Other. One bullet per commit,
# oldest first, the sha in parentheses, and the "type:" or "type(scope):"
# prefix stripped. A merge or a landing commit stays in Other with its subject
# whole, because that subject is the record of what landed.
#
# Bash only: the workflow runs it on a runner with git and nothing else.
set -euo pipefail

prev=${1:-}
tag=${2:-}

if [ -z "$tag" ]; then
	tag=$(git describe --tags --abbrev=0 2>/dev/null || printf 'HEAD')
fi

# The previous release is the nearest tag behind this one, not the highest tag
# in the repository: a notes file talks about one line of history.
if [ -z "$prev" ]; then
	prev=$(git describe --tags --abbrev=0 "$tag^" 2>/dev/null || true)
fi

if [ -n "$prev" ]; then
	range="$prev..$tag"
else
	range="$tag"
fi

feat=()
fix=()
perf=()
docs=()
other=()

while IFS=$'\x1f' read -r sha parents subject; do
	if [ -z "$sha" ]; then
		continue
	fi
	type=""
	body=$subject
	# A merge commit, and the commits the landing tool writes, carry no useful
	# type. Keep their subject whole under Other.
	case "$subject" in
	Merge\ *|land\ *|land:|dungeon:\ commit\ lane\ work*) other+=("- $subject ($sha)"); continue ;;
	esac
	# Two parents is a merge. One is every ordinary commit.
	case "$parents" in
	*" "*)
		other+=("- $subject ($sha)")
		continue
		;;
	esac
	# "type(scope): subject" and "type: subject" both yield "subject".
	if [[ $subject =~ ^([A-Za-z]+)(\([^\)]*\))?:[[:space:]]*(.*)$ ]]; then
		type=${BASH_REMATCH[1],,}
		body=${BASH_REMATCH[3]}
	fi
	case "$type" in
	feat) feat+=("- $body ($sha)") ;;
	fix) fix+=("- $body ($sha)") ;;
	perf) perf+=("- $body ($sha)") ;;
	docs) docs+=("- $body ($sha)") ;;
	*) other+=("- $body ($sha)") ;;
	esac
done < <(git log --reverse --format="%h%x1f%p%x1f%s" "$range")

printf '## New in %s\n' "$tag"

section() {
	local title=$1
	shift
	local item
	if [ "$#" -eq 0 ]; then
		return
	fi
	printf '\n### %s\n\n' "$title"
	for item in "$@"; do
		printf '%s\n' "$item"
	done
}

section Features "${feat[@]}"
section Fixes "${fix[@]}"
section Performance "${perf[@]}"
section Docs "${docs[@]}"
section Other "${other[@]}"
