// Command sonarc is a modeless terminal code editor for Linux servers.
//
// It is built for ssh into tmux: every command is reachable without relying on
// key combinations a multiplexer cannot report, and it degrades to something
// usable rather than something broken on a bare console.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/sonarc-dev/sonarc/internal/term"
	"github.com/sonarc-dev/sonarc/internal/update"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	var (
		showVersion = flag.Bool("version", false, "print version and exit")
		doctor      = flag.Bool("doctor", false, "report terminal capabilities and suggest fixes")
		lineNo      = flag.Int("line", 0, "put the cursor on this line")
		traceInput  = flag.String("trace-input", "", "log terminal input and the events made of it to `file`")
		doUpdate    = flag.Bool("update", false, "replace this binary with the latest release")
	)
	flag.Usage = usage
	flag.Parse()

	switch {
	case *showVersion:
		fmt.Printf("sonarc %s\n", version)
		return
	case *doctor:
		term.Doctor(os.Stdout)
		return
	case *doUpdate:
		exe, err := executable()
		if err != nil {
			fmt.Fprintf(os.Stderr, "sonarc: %v\n", err)
			os.Exit(1)
		}
		os.Exit(runUpdate(os.Stdout, update.Source{}, exe))
	}

	if *traceInput != "" {
		f, err := os.Create(*traceInput)
		if err != nil {
			fmt.Fprintf(os.Stderr, "sonarc: %v\n", err)
			os.Exit(1)
		}
		defer f.Close()
		term.TraceInput = f
	}

	if err := run(flag.Args(), *lineNo); err != nil {
		fmt.Fprintf(os.Stderr, "sonarc: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `sonarc %s - a terminal code editor that doesn't get in your way

usage: sonarc [options] [file|directory]...

  sonarc file.c        edit a file
  sonarc file.c:42     edit a file at line 42 (file.c:42:7 for a column too)
  sonarc .             open the current directory as a project
  sonarc ~/src/proj    open a directory; its file tree appears on the left

options:
  -line N     put the cursor on line N (ignored when only a directory is given)
  -doctor     report terminal capabilities and suggest fixes
  -trace-input FILE
              log what the terminal sends, to diagnose keys or clicks that
              do nothing
  -update     replace this binary with the latest release
  -version    print version and exit

Inside the editor, press F1 for the keys that work on this terminal.
`, version)
}
