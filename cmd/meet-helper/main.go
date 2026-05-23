// ABOUTME: meet-helper CLI shim (issue #8). Invokes any meet subcommand on
// ABOUTME: a remote host via ssh with the canonical deploy-time config cascade.

package main

import (
	_ "embed"
	"fmt"
	"os"
	"os/exec"

	"github.com/tadg-paul/meet/internal/sshshim"
)

// helpText is copied from docs/help/meet-helper.txt by the Makefile build
// target. Editing the inline copy here is unsupported — edit the docs
// source and rebuild.
//
//go:embed help.txt
var helpText string

// Version is the build identifier, overridden at link time.
var Version = "dev"

func main() {
	args := os.Args[1:]

	// Local help / version recognition: -h, --help, --version are
	// handled locally when they appear before a subcommand has been
	// named (positions 0 or 1). Once a subcommand has been named, any
	// further -h is forwarded to the remote subcommand so callers can
	// reach `meet token -h`, `meet create -h`, etc.
	for i, a := range args {
		if i > 1 {
			break
		}
		switch a {
		case "--version", "-version":
			fmt.Println(Version)
			os.Exit(0)
		case "-h", "--help":
			printUsage(os.Stdout)
			os.Exit(0)
		}
	}

	if len(args) < 2 {
		printUsage(os.Stderr)
		os.Exit(2)
	}

	host := args[0]
	subcommand := args[1]
	extra := args[2:]

	argv := sshshim.BuildSSHArgv(host, subcommand, extra)
	cmd := exec.Command("ssh", argv...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func printUsage(out *os.File) {
	fmt.Fprint(out, helpText)
}
