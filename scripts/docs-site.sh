#!/usr/bin/env bash
# docs-site.sh stages the published markdown and builds the GitHub Pages site.
#
#   scripts/docs-site.sh            stage into .site/ and build site/
#   scripts/docs-site.sh --stage    stage only, write no site
#
# The allowlist below is the published surface. A working note, a review log or
# a developer README stays out by not being named, so nothing leaks onto the
# site because it happened to be markdown in the tree.
#
# The staging tree mirrors the repo layout, which is what keeps the relative
# links between pages working unchanged. Only tracked files are read, so an
# ignored scratch file cannot appear either.
set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"

stage=.site
rm -rf "$stage"
mkdir -p "$stage"

staged=0
while IFS= read -r -d '' file; do
	case "$file" in
	README.md | PLUGINS.md | docs/internals/* | docs/releases/*)
		install -D -m 0644 "$file" "$stage/$file"
		staged=$((staged + 1))
		;;
	# A skill is a directory whose page is SKILL.md or README.md. Both stage as
	# index.md, so the page answers at /skills/<name>/ rather than at
	# /skills/<name>/SKILL/. The repo skill also moves out of .agents, which the
	# build ignores as a dot directory.
	skills/dm-fleet/SKILL.md | skills/dm-agent/README.md)
		name=${file#skills/}
		install -D -m 0644 "$file" "$stage/skills/${name%/*}/index.md"
		staged=$((staged + 1))
		;;
	.agents/skills/*/SKILL.md)
		name=${file#.agents/skills/}
		install -D -m 0644 "$file" "$stage/skills/${name%/SKILL.md}/index.md"
		staged=$((staged + 1))
		;;
	esac
done < <(git ls-files -z -- '*.md')

if [ "$staged" -eq 0 ]; then
	echo "docs-site: no page matched the allowlist" >&2
	exit 1
fi
if [ ! -f "$stage/README.md" ]; then
	echo "docs-site: README.md is not staged, so the site would have no home" >&2
	exit 1
fi

# The README links its licence, so the site carries it beside the pages.
if [ -f LICENSE ]; then
	install -D -m 0644 LICENSE "$stage/LICENSE"
fi

if [ "${1:-}" = "--stage" ]; then
	printf 'docs-site: staged %d pages in %s/\n' "$staged" "$stage"
	exit 0
fi

exec zensical build
