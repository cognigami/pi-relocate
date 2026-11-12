package main

import (
	"fmt"
	"io"
	"os"

	"pi-relocate/internal/relocate"
)

const usage = "Usage: relo [--dry-run] SOURCE DESTINATION\n       relo --help\n"

func run(args []string, out, diagnostic io.Writer) int {
	if len(args) == 1 && args[0] == "--help" {
		fmt.Fprint(out, usage)
		return 0
	}
	dry := false
	if len(args) > 0 && args[0] == "--dry-run" {
		dry = true
		args = args[1:]
	}
	if len(args) != 2 || len(args[0]) == 0 || len(args[1]) == 0 || args[0][0] == '-' || args[1][0] == '-' {
		fmt.Fprint(diagnostic, usage)
		return 1
	}
	options, err := relocate.DefaultOptions()
	if err != nil {
		fmt.Fprintf(diagnostic, "Refused: %v\nNo changes made\n", err)
		return 1
	}
	plan, err := relocate.Discover(args[0], args[1], options)
	if err != nil {
		fmt.Fprintf(diagnostic, "Refused: %v\nNo changes made\n", err)
		return 1
	}
	plan.Preview(out)
	if dry {
		fmt.Fprintln(out, "Preview validated. No changes made.")
		return 0
	}
	if err := plan.Execute(out); err != nil {
		fmt.Fprintf(diagnostic, "Relocation failed: %v\n", err)
		return 1
	}
	fmt.Fprintln(out, "Verify discovery and resume with pi -r in the destination; interactive acceptance is pending.")
	fmt.Fprintf(out, "Reminder: start Pi in %s and use /session-index to rebuild the session index.\n", plan.Destination)
	return 0
}

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
