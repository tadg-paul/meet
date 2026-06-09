// ABOUTME: meet-helper CLI shim. Invokes any meet subcommand on a remote
// ABOUTME: NixOS host via ssh and the deploy-nix meet-admin wrapper.

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
	// named (positions 0 or 1). Subcommand help is handled below from
	// the local companion meet binary so help never depends on remote
	// config or secrets loading.
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

	if containsHelpFlag(extra) {
		runLocalSubcommandHelp(subcommand)
		return
	}

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

func containsHelpFlag(args []string) bool {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			return true
		}
	}
	return false
}

func runLocalSubcommandHelp(subcommand string) {
	cmd := exec.Command("meet", subcommand, "--help")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		fmt.Fprintf(os.Stderr, "error: local meet help: %v\n", err)
		os.Exit(1)
	}
}
