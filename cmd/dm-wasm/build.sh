#!/bin/sh
# Build the browser spike into a folder a static server can serve.
#
#   cmd/dm-wasm/build.sh out
#   python3 -m http.server -d out 8765
#
# The page needs the network once, to fetch the terminal renderer. After that
# the folder is self-contained.
set -eu

out=${1:-out}
mkdir -p "$out/vendor"

GOOS=js GOARCH=wasm go build -trimpath -o "$out/dm.wasm" ./cmd/dm-wasm

# The loader ships with the toolchain that built the wasm, and its path moved
# in Go 1.24.
root=$(go env GOROOT)
loader=$root/lib/wasm/wasm_exec.js
[ -f "$loader" ] || loader=$root/misc/wasm/wasm_exec.js
cp "$loader" "$out/wasm_exec.js"

# Pinned renderer. sip's xterm.js bundle is what tuios ships for the same job.
xterm=https://cdn.jsdelivr.net/npm/@xterm
fetch() { [ -s "$2" ] || curl -fsSL -o "$2" "$1"; }
fetch "$xterm/xterm@5.5.0/lib/xterm.js" "$out/vendor/xterm.js"
fetch "$xterm/xterm@5.5.0/css/xterm.css" "$out/vendor/xterm.css"
fetch "$xterm/addon-fit@0.10.0/lib/addon-fit.js" "$out/vendor/addon-fit.js"

# The font the client was drawn for. JetBrains Mono has the box-drawing and
# block glyphs the frames are built from; a system fallback leaves gaps.
mono=https://cdn.jsdelivr.net/gh/JetBrains/JetBrainsMono@v2.304/fonts/webfonts
fetch "$mono/JetBrainsMono-Regular.woff2" "$out/vendor/JetBrainsMono-Regular.woff2"
fetch "$mono/JetBrainsMono-Bold.woff2" "$out/vendor/JetBrainsMono-Bold.woff2"

cp cmd/dm-wasm/index.html "$out/index.html"
printf 'built %s (%s bytes)\n' "$out/dm.wasm" "$(wc -c <"$out/dm.wasm" | tr -d ' ')"