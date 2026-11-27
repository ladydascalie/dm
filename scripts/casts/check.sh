#!/usr/bin/env bash
# check.sh fails unless each cast is a whole recording the page can play.
#
#   scripts/casts/check.sh docs/casts/*.cast
#
# A cast is checked for five things, in this order:
#   - the header line parses as an asciicast version 2 with a width and a height
#   - every event line parses as JSON
#   - the cast carries at least one input event, so a feed ran
#   - the last frame is not empty, so the recording ends on a screen and not on
#     the restore sequence a detach would leave
#   - every codepoint above U+007F it draws has a shape in the webfont that
#     ships beside it, so the page shows no tofu
#
# record.sh runs this on each cast it writes, so a fresh cast cannot land
# unplayable or undrawable.
set -euo pipefail

# The cast paths are repo paths, so the script works from anywhere.
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$root"

font=docs/assets/font/dm-cast-mono.woff2

if [ $# -eq 0 ]; then
	echo "usage: scripts/casts/check.sh <cast...>" >&2
	exit 2
fi
if [ ! -f "$font" ]; then
	printf 'check: no %s. Run make docs-font.\n' "$font" >&2
	exit 1
fi

uvx --quiet --with fonttools python - "$font" "$@" <<'PY'
import json
import re
import sys

from fontTools.ttLib import TTFont

font_path, casts = sys.argv[1], sys.argv[2:]

# One pass over the bytes dm wrote: the escapes go, the text stays. The
# alternative-screen restore is kept apart, because the text after the last one
# is the screen the cast leaves behind.
ESCAPE = re.compile(
    r'\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)'   # OSC
    r'|\x1bP.*?\x1b\\'                      # DCS
    r'|\x1b\[[0-9;?<=>]*[ -/]*[@-~]'        # CSI
    r'|\x1b[@-Z\\-_]'                       # two-byte escape
    r'|\x1b.',                              # anything else that starts one
    re.S,
)
RESTORE = re.compile(r'\x1b\[\?1049l|\x1b\[\?47l')
# \n and \r are not text on a screen: a row is drawn by its cursor moves.
BLANK = re.compile(r'[\x00-\x1f\x7f]')

have = set(TTFont(font_path).getBestCmap())
failed = 0
problems = 0


def fail(name, problem):
    global failed, problems
    failed = problems = 1
    print(f'check: {name}: {problem}')


for path in casts:
    problems = 0
    try:
        text = open(path, encoding='utf-8').read()
    except OSError as err:
        fail(path, err.strerror or str(err))
        continue
    lines = text.splitlines()
    if not lines:
        fail(path, 'is empty')
        continue

    try:
        head = json.loads(lines[0])
    except ValueError:
        fail(path, 'does not start with a JSON header line')
        continue
    if not isinstance(head, dict) or head.get('version') != 2:
        fail(path, 'header is not an asciicast version 2 record')
        continue
    cols, rows = head.get('width'), head.get('height')
    if not all(isinstance(v, int) and v > 0 for v in (cols, rows)):
        fail(path, 'header carries no width and height')
        continue

    events, drawn, inputs, broke = [], set(), 0, False
    for n, line in enumerate(lines[1:], start=2):
        if not line.strip():
            continue
        try:
            event = json.loads(line)
        except ValueError:
            fail(path, f'line {n} is not JSON')
            broke = True
            break
        ok = (
            isinstance(event, list)
            and len(event) == 3
            and isinstance(event[0], (int, float))
            and not isinstance(event[0], bool)
            and isinstance(event[1], str)
            and isinstance(event[2], str)
        )
        if not ok:
            fail(path, f'line {n} is not [time, type, data]')
            broke = True
            break
        events.append(event)
        if event[1] == 'i':
            inputs += 1
        elif event[1] == 'o':
            drawn.update(map(ord, event[2]))
    if broke:
        continue
    if not events:
        fail(path, 'carries no events')
        continue
    if inputs == 0:
        fail(path, 'carries no input event, so no feed ran')

    # The frame the cast leaves: everything drawn after the last alt-screen
    # restore. A detach leaves the restore last and nothing after it.
    stream = ''.join(e[2] for e in events if e[1] == 'o')
    last = RESTORE.split(stream)[-1]
    if not BLANK.sub('', ESCAPE.sub('', last)).strip():
        fail(path, 'ends on an empty frame')

    gaps = sorted(c for c in drawn if c > 0x7F and c not in have)
    if gaps:
        fail(path, 'draws %s, which the webfont lacks. Run make docs-font.'
             % ' '.join('U+%04X' % c for c in gaps))
        continue

    if problems:
        continue
    end = events[-1][0]
    print('check: %s: ok: %d events, ends at %.1fs, %dx%d, %d bytes'
          % (path, len(events), end, cols, rows, len(text.encode('utf-8'))))

sys.exit(failed)
PY
