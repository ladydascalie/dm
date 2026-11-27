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
# A page may also name a file beside it: a recorded cast and the player that
# plays it. Those stage by the same allowlist, and a page that names a cast
# which did not stage fails the build. The link checker cannot see that one,
# because a cast is named in an attribute rather than in a link.
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

staged_assets=0
while IFS= read -r -d '' file; do
	install -D -m 0644 "$file" "$stage/$file"
	staged_assets=$((staged_assets + 1))
done < <(git ls-files -z -- 'docs/casts/*.cast' 'docs/assets/*' 'docs/javascripts/*.js')

if [ "$staged" -eq 0 ]; then
	echo "docs-site: no page matched the allowlist" >&2
	exit 1
fi
if [ ! -f "$stage/README.md" ]; then
	echo "docs-site: README.md is not staged, so the site would have no home" >&2
	exit 1
fi

# A page names a cast by its path in the repo, and the browser resolves that
# path against the site root. A name with no file behind it is a dead player,
# and nothing else in the build would notice.
missing=0
while IFS= read -r -d '' page; do
	while IFS= read -r cast; do
		[ -n "$cast" ] || continue
		if [ ! -f "$stage/$cast" ]; then
			printf 'docs-site: %s plays %s, which is not staged\n' "${page#"$stage/"}" "$cast" >&2
			missing=1
		fi
	done < <(grep -o 'data-cast="[^"]*"' "$page" | sed 's|^data-cast="||; s|"$||')
done < <(find "$stage" -name '*.md' -print0)
if [ "$missing" -ne 0 ]; then
	exit 1
fi

# The README links its licence, so the site carries it beside the pages.
if [ -f LICENSE ]; then
	install -D -m 0644 LICENSE "$stage/LICENSE"
fi

if [ "${1:-}" = "--stage" ]; then
	printf 'docs-site: staged %d pages and %d assets in %s/\n' "$staged" "$staged_assets" "$stage"
	exit 0
fi

exec zensical build
