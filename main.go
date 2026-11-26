package main

import (
	"fmt"
	"os"
	"strings"

	"dm/internal/cli"
	"dm/internal/clidoc"
	"dm/internal/client"
	"dm/internal/pane"
	"dm/internal/server"
)

func main() {
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "__server":
			exit(server.Run(args[1:]))
			return
		case "__pane":
			exit(pane.Run(args[1:]))
			return
		case "__probe":
			exit(runProbe(args[1:]))
			return
		case "__update-check":
			client.RunUpdateCheck()
			return
		case "-h", "--help", "help":
			if len(args) == 1 {
				fmt.Print(clidoc.UsageText())
				return
			}
			topic := strings.Join(args[1:], " ")
			if topic == "msg" {
				fmt.Print(clidoc.MsgSubcommands())
				return
			}
			if help := clidoc.VerbHelp(topic); help != "" {
				fmt.Print(help)
				return
			}
			exit(fmt.Errorf("no help topic %q", topic))
			return
		}
	}
	exit(cli.Run(args))
}

func exit(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "dm:", err)
		os.Exit(1)
	}
}
