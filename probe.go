package main

import (
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"golang.org/x/term"
)

// runProbe speaks the program status protocol on the pane's terminal. It is a
// test harness: send the feature query, wait for the terminal's reply, report
// working, hold, then clear. `dm __probe` prints its verdicts on the pane.
func runProbe(args []string) error {
	fs := flag.NewFlagSet("__probe", flag.ContinueOnError)
	hold := fs.Duration("hold", 2*time.Second, "how long to stay working")
	if err := fs.Parse(args); err != nil {
		return err
	}

	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		if old, err := term.MakeRaw(fd); err == nil {
			defer term.Restore(fd, old)
		}
	}

	out := func(s string) { fmt.Print(strings.ReplaceAll(s, "\n", "\r\n")) }

	out("\x1b]7501;?\x1b\\")
	reply := make(chan string, 1)
	go func() {
		var got strings.Builder
		buf := make([]byte, 64)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				got.Write(buf[:n])
				if strings.Contains(got.String(), "\x1b]7501;?") {
					reply <- got.String()
					return
				}
			}
			if err != nil {
				reply <- got.String()
				return
			}
		}
	}()

	select {
	case got := <-reply:
		if strings.Contains(got, "\x1b]7501;?") {
			out("PROBE-REPLY=ok\n")
		} else {
			out("PROBE-REPLY=bad\n")
		}
	case <-time.After(700 * time.Millisecond):
		out("PROBE-REPLY=missing\n")
	}

	msg := base64.StdEncoding.EncodeToString([]byte("probe reporting"))
	fmt.Printf("\x1b]7501;state=working:app=dmprobe:msg=%s\x1b\\", msg)
	out("PROBE-REPORTED\n")
	time.Sleep(*hold)
	os.Stdout.WriteString("\x1b]7501;state=clear\x1b\\")
	out("PROBE-CLEARED\n")
	return nil
}
