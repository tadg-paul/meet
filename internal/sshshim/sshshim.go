// ABOUTME: Pure helpers for the meet-helper SSH shim. Construct the argv
// ABOUTME: that ssh will execute on a NixOS remote host via meet-admin.

package sshshim

import "strings"

// RemoteAppUser is the app identity used by the NixOS admin wrapper.
const RemoteAppUser = "meet"

// RemoteAdminCommand is the NixOS deploy-nix admin wrapper for meet.
const RemoteAdminCommand = "meet-admin"

// BuildSSHArgv returns the full argv that should be handed to exec.Command
// for the ssh process. The first element is the hostname (ssh's positional
// argument) and the second is a single shell-safely-quoted command string
// containing the remote meet invocation. Tests call this directly.
//
// Example:
//
//	BuildSSHArgv("light-hugger", "create",
//	    []string{"--room", "demo", "--from", "2026-05-25T19:00:00Z"})
//	-> []string{"light-hugger",
//	            "'sudo' '-u' 'meet' 'meet-admin' 'create' '--room' 'demo' '--from' '2026-05-25T19:00:00Z'"}
func BuildSSHArgv(host, subcommand string, extra []string) []string {
	parts := []string{
		shellQuote("sudo"),
		shellQuote("-u"),
		shellQuote(RemoteAppUser),
		shellQuote(RemoteAdminCommand),
		shellQuote(subcommand),
	}
	for _, a := range extra {
		parts = append(parts, shellQuote(a))
	}
	return []string{host, strings.Join(parts, " ")}
}

// shellQuote wraps s in posix-sh single quotes, escaping any embedded single
// quotes via the standard 'foo'\”bar' idiom. Safe for use as a remote shell
// argument under ssh, which evaluates the command line through the user's
// remote login shell.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
