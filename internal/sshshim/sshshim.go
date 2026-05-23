// ABOUTME: Pure helpers for the meet-helper SSH shim (issue #8). Construct
// ABOUTME: the argv that ssh will execute on a remote host for any meet
// ABOUTME: subcommand, using the canonical deploy-time config cascade.

package sshshim

import (
	"fmt"
	"strings"
)

// RemoteMeetBinary is the path the deploy convention installs the meet binary
// at on every host. Exported as a constant so tests can reference the same
// literal the shim emits.
const RemoteMeetBinary = "/srv/meet/meet"

// RemoteSecretsPath is the canonical secrets path on every deployed host.
const RemoteSecretsPath = "/etc/meet/secrets.yaml"

// ConfigCascade returns the comma-separated --config value used by every
// remote invocation. Layers: defaults.yaml, the per-host config file, then
// the host's secrets.
func ConfigCascade(host string) string {
	return fmt.Sprintf(
		"/srv/meet/repo/config/defaults.yaml,/srv/meet/repo/config/%s.yaml,%s",
		host, RemoteSecretsPath,
	)
}

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
//	            "'/srv/meet/meet' '--config' '/srv/meet/repo/config/defaults.yaml,/srv/meet/repo/config/light-hugger.yaml,/etc/meet/secrets.yaml' 'create' '--room' 'demo' '--from' '2026-05-25T19:00:00Z'"}
func BuildSSHArgv(host, subcommand string, extra []string) []string {
	parts := []string{
		shellQuote(RemoteMeetBinary),
		shellQuote("--config"),
		shellQuote(ConfigCascade(host)),
		shellQuote(subcommand),
	}
	for _, a := range extra {
		parts = append(parts, shellQuote(a))
	}
	return []string{host, strings.Join(parts, " ")}
}

// shellQuote wraps s in posix-sh single quotes, escaping any embedded single
// quotes via the standard 'foo'\''bar' idiom. Safe for use as a remote shell
// argument under ssh, which evaluates the command line through the user's
// remote login shell.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
