# project: the project prompt regroups the rail, and the group outlives the
# session.
#
# Sourced by scripts/casts/record.sh. cols, rows and seconds are required and
# feed() writes the keystrokes to stdout, through key, say and run.

cols=132
rows=34
seconds=17

feed() {
	sleep 1.8
	key '\x02' 1.6
	key 'P' 1.8     # the project prompt
	say 'go-backend' 0.5
	key '\r' 2.4    # the rail regroups
	run 'dm ls' 2.6 # the project outlives the session
	key '\r' 1.4    # a prompt row under the table, so the cast ends on it
}
