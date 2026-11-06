package main

import (
	"fmt"
	"os"

	"dm/internal/cli"
	"dm/internal/clidoc"
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
		case "-h", "--help", "help":
			fmt.Print(clidoc.UsageText())
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
