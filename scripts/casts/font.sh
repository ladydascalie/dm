#!/usr/bin/env bash
# font.sh builds the one webfont the docs site plays a cast in.
#
#   scripts/casts/font.sh
#
# A cast carries the bytes dm wrote, and dm writes Nerd Font glyphs into its
# rail and status cells. A reader without a patched font gets tofu where those
# glyphs sit, and a browser cannot borrow the reader's terminal font. So the
# site ships a subset: the codepoints dm can draw, taken from the Go sources and
# from the recorded casts, cut out of the upstream Nerd Font build.
#
# The subset is a modified font, so it cannot carry the upstream name. It ships
# as "dm Cast Mono" with the copyright and the SIL Open Font License records
# intact, and OFL.txt beside it, as the licence requires.
set -euo pipefail

root=$(git rev-parse --show-toplevel)
cd "$root"

version=3.3.0
url=https://github.com/ryanoasis/nerd-fonts/raw/v$version/patched-fonts/JetBrainsMono/Ligatures/Regular/JetBrainsMonoNerdFont-Regular.ttf
license_url=https://raw.githubusercontent.com/JetBrains/JetBrainsMono/master/OFL.txt

cache=${XDG_CACHE_HOME:-$HOME/.cache}/dm-casts
src=$cache/JetBrainsMonoNerdFont-Regular.ttf
out=docs/assets/font/dm-cast-mono.woff2

mkdir -p "$cache" docs/assets/font
if [ ! -f "$src" ]; then
	curl -sSL --max-time 120 -o "$src" "$url"
fi
if [ ! -f docs/assets/font/OFL.txt ]; then
	curl -sSL --max-time 60 -o docs/assets/font/OFL.txt "$license_url"
fi

uvx --quiet --with fonttools --with brotli python - "$src" "$out" "$version" <<'PY'
import glob, os, re, sys
from fontTools import subset
from fontTools.ttLib import TTFont

src, out, version = sys.argv[1], sys.argv[2], sys.argv[3]

# Everything dm can put on a screen: the printable ASCII dm writes, the runes
# the Go sources hold (literal and \uXXXX escaped), and every codepoint the
# recorded casts already carry.
codes = set(range(0x20, 0x7f))
for pattern in ('internal/**/*.go', 'cmd/**/*.go'):
    for path in glob.glob(pattern, recursive=True):
        if path.endswith('_test.go'):
            continue
        text = open(path, encoding='utf-8', errors='ignore').read()
        codes.update(ord(ch) for ch in text if ord(ch) > 0x7f)
        for esc in re.findall(r'\\u([0-9a-fA-F]{4})|\\U([0-9a-fA-F]{8})', text):
            codes.add(int(esc[0] or esc[1], 16))
for path in glob.glob('docs/casts/*.cast'):
    text = open(path, encoding='utf-8', errors='ignore').read()
    codes.update(ord(ch) for ch in text if ord(ch) > 0x7f)

font = TTFont(src)
have = set(font.getBestCmap())
wanted = sorted(c for c in codes if c in have)

opts = subset.Options()
opts.flavor = 'woff2'
opts.layout_features = []
opts.name_IDs = ['*']
opts.name_legacy = True
opts.notdef_outline = True
opts.recalc_bounds = True
opts.drop_tables += ['DSIG', 'PfEd']
sub = subset.Subsetter(options=opts)
sub.populate(unicodes=wanted)
sub.subset(font)

# The name records this subset owns. Everything else, the copyright and the
# licence included, stays as upstream wrote it.
renames = {
	'1': 'dm Cast Mono',
	'16': 'dm Cast Mono',
	'2': 'Regular',
	'17': 'Regular',
	'4': 'dm Cast Mono Regular',
	'6': 'dmCastMono-Regular',
	'3': 'dmCastMono-Regular;' + version + ';subset',
}
for rec in font['name'].names:
    if str(rec.nameID) in renames:
        rec.string = renames[str(rec.nameID)]

font.save(out)
print('font: %d codepoints, %d bytes' % (len(wanted), os.path.getsize(out)))
PY
