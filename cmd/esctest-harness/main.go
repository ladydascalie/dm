// Command esctest-harness runs esctest2 against the dm VT.
//
// The log goes to stdout. Arguments the harness does not recognise are passed
// to esctest.py, for example --include=DECRQCRA.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"dm/internal/esctest"
)

func main() {
	var (
		verbose bool
		dir     string
		args    []string
	)
	rest := os.Args[1:]
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch {
		case a == "--verbose" || a == "-verbose":
			verbose = true
		case a == "--esctest-dir" || a == "-esctest-dir":
			if i+1 >= len(rest) {
				usage()
				os.Exit(2)
			}
			i++
			dir = rest[i]
		case strings.HasPrefix(a, "--esctest-dir="):
			dir = strings.TrimPrefix(a, "--esctest-dir=")
		case strings.HasPrefix(a, "-esctest-dir="):
			dir = strings.TrimPrefix(a, "-esctest-dir=")
		case a == "--help" || a == "-h":
			usage()
			return
		default:
			args = append(args, a)
		}
	}

	code, err := esctest.Run(context.Background(), esctest.Options{
		Dir:     dir,
		Args:    args,
		Verbose: verbose,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "esctest-harness:", err)
		os.Exit(1)
	}
	os.Exit(code)
}

func usage() {
	fmt.Fprint(os.Stderr, `usage: esctest-harness [--verbose] [--esctest-dir DIR] [esctest args...]

  --verbose          name each sequence the harness answers
  --esctest-dir DIR  esctest2 directory (default: $ESCTEST_DIR, then
                     internal/esctest/testdata/esctest, then /tmp/esctest2)

Other arguments are passed to esctest.py, for example --include=DECRQCRA.
The log goes to stdout.
`)
}
