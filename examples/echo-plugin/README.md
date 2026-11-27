# echo plugin

The smallest useful plugin: it posts `ready` on `plugin/echo/state`, watches
`plugin/echo/cmd/` and echoes each command back on the state board.

Declare it, with an absolute path, and bind a key:

```kdl
plugin "echo" {
    exec "/home/you/src/dm/examples/echo-plugin/echo.sh"
    where "split"
}
keymap "main" {
    "plugin:echo" "e"
}
```

Then:

```sh
dm plugin start echo
dm msg post --key plugin/echo/cmd/one --state note --msg hello
dm msg list --prefix plugin/echo/
```

It needs `jq`. The cursor lives in `DM_PLUGIN_STATE_DIR`, so a server restart
resumes where the last entry ended. `PLUGINS.md` at the repository root is the
contract.