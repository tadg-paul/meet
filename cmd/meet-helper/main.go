// ABOUTME: meet-helper CLI shim (issue #8). Invokes any meet subcommand on
// ABOUTME: a remote host via ssh with the canonical deploy-time config cascade.

package main

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/tadg-paul/meet/internal/sshshim"
)

// Version is the build identifier, overridden at link time.
var Version = "dev"

func main() {
	args := os.Args[1:]

	// Handle version + help before positional parsing.
	if len(args) >= 1 {
		switch args[0] {
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
	fmt.Fprintln(out, "Usage: meet-helper <host> <subcommand> [args...]")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Invoke any meet subcommand on a remote deploy host via SSH. The")
	fmt.Fprintln(out, "config cascade (defaults.yaml + per-host config + secrets) is supplied")
	fmt.Fprintln(out, "automatically using the deploy convention.")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "Examples:")
	fmt.Fprintln(out, "  meet-helper light-hugger token --room workshop-april")
	fmt.Fprintln(out, "  meet-helper light-hugger create --room demo \\")
	fmt.Fprintln(out, "    --from 2026-05-25T19:00:00Z --until 2026-05-25T21:00:00Z")
	fmt.Fprintln(out, "  meet-helper light-hugger list --filter active")
	fmt.Fprintln(out, "  meet-helper light-hugger cancel --room demo")
	fmt.Fprintln(out, "")
	fmt.Fprintln(out, "The host must have meet deployed at /srv/meet/meet with config and")
	fmt.Fprintln(out, "secrets at the standard paths.")
}
