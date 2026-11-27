#!/usr/bin/env bash
# hex-vendor.sh copies the hex theme's published files into docs/assets/hex.
#
#   scripts/hex-vendor.sh            vendor the tag below
#   scripts/hex-vendor.sh v0.1.1     vendor another tag
#
# The files come from the tag's GitHub release, so this reads what hex actually
# published and not a working copy. The docs build itself fetches nothing: the
# files are committed here, and this script is how they move.
#
# docs/assets/hex/hex.css stays dm's own, because it maps the tokens onto
# Material, which is this site's stack and not the theme's.
#
# HEX_SOURCE points at a local hex checkout, for a tag that is not published
# yet. It reads that checkout at the tag instead of the release.
set -euo pipefail

tag=${1:-v0.1.0}
repo=${HEX_REPO:-ladydascalie/hex}
URL_BASE="https://github.com/$repo/releases/download/$tag"
root=$(git rev-parse --show-toplevel)
out=$root/docs/assets/hex

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

if [ -n "${HEX_SOURCE:-}" ]; then
	if ! git -C "$HEX_SOURCE" rev-parse --verify --quiet "$tag^{commit}" >/dev/null; then
		printf 'hex-vendor: %s has no tag %s\n' "$HEX_SOURCE" "$tag" >&2
		exit 1
	fi
	git -C "$HEX_SOURCE" archive "$tag" tokens.css fonts.css LICENSE fonts | tar -x -C "$tmp"
else
	for file in tokens.css fonts.css fonts.tar.gz LICENSE; do
		if ! curl -fsSL --retry 2 -o "$tmp/$file" "$URL_BASE/$file"; then
			printf 'hex-vendor: %s is not a published release of %s\n' "$tag" "$repo" >&2
			exit 1
		fi
	done
	if ! tar xzf "$tmp/fonts.tar.gz" -C "$tmp"; then
		printf 'hex-vendor: the fonts bundle in %s will not unpack\n' "$tag" >&2
		exit 1
	fi
fi

# A partial copy would ship a theme with no colours, and the site would build
# and look broken. The check runs before anything here is replaced.
for want in tokens.css fonts.css LICENSE \
	fonts/atkinson-hyperlegible-next-latin-wght-normal.woff2 \
	fonts/atkinson-hyperlegible-mono-latin-wght-normal.woff2 \
	fonts/noto-sans-mono-shades.woff2; do
	if [ ! -f "$tmp/$want" ]; then
		printf 'hex-vendor: %s is not in %s\n' "$want" "$tag" >&2
		exit 1
	fi
done
if ! grep -q -- '--accent:' "$tmp/tokens.css"; then
	printf 'hex-vendor: tokens.css in %s carries no accent token\n' "$tag" >&2
	exit 1
fi

# Only the vendored files move. hex.css is this site's adapter.
rm -rf "$out/fonts" "$out/tokens.css" "$out/fonts.css" "$out/LICENSE" "$out/PROVENANCE"
mkdir -p "$out"
install -m 0644 "$tmp/tokens.css" "$tmp/fonts.css" "$tmp/LICENSE" "$out/"
cp -a "$tmp/fonts" "$out/fonts"

cat >"$out/PROVENANCE" <<EOF
hex
===

tokens.css, fonts.css, LICENSE and fonts/ are copied from the hex repository at
tag $tag:

  https://github.com/$repo

DESIGN.md there is the contract for these tokens. hex.css beside this file is
not vendored: it maps the tokens onto Material, which is this site's stack.

Move the pin with ./scripts/hex-vendor.sh <tag>, update the default in that
script, then rebuild the site and read both tiers.
EOF

printf 'hex-vendor: vendored hex %s into %s\n' "$tag" "${out#"$root"/}"