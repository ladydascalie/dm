#!/bin/sh
# echo: post every command entry back on the state board. A plugin's whole
# interface is the dm CLI and the box; this file is the smallest proof.
set -u
id=${DM_PLUGIN_ID:?run me from a dm plugin pane}
dir=${DM_PLUGIN_STATE_DIR:?run me from a dm plugin pane}
cursor="$dir/cursor"
[ -e "$cursor" ] || printf '0\n' > "$cursor"

dm msg post --key "plugin/$id/state" --state note --msg "ready" || exit 1

# The box connection ends when the server restarts, so the loop re-establishes
# it from the last seq. A plugin that must not miss an entry keeps its cursor.
while :; do
  after=$(cat "$cursor")
  dm msg sub --prefix "plugin/$id/cmd/" --after "$after" --json |
    while IFS= read -r line; do
      seq=$(printf '%s' "$line" | jq -r .seq)
      text=$(printf '%s' "$line" | jq -r .msg)
      printf '%s\n' "$seq" > "$cursor"
      dm msg post --key "plugin/$id/state" --state note --msg "echo: $text"
    done
  sleep 0.2
done