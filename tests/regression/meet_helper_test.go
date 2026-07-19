// ABOUTME: Regression tests for the meet-helper SSH shim (issues #8 and #10).
// ABOUTME: Verifies argv construction directly via the exported function
// ABOUTME: (no SSH connection opened) plus a small set of CLI exec tests
// ABOUTME: that exercise help/usage and exit codes.

package regression

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tigger-developer/meet/internal/sshshim"
)

// meetHelperBin is populated by ensureMeetHelperBin (lazy build).
var meetHelperBin string

func ensureMeetHelperBin(t *testing.T) string {
	t.Helper()
	if meetHelperBin != "" {
		return meetHelperBin
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoRoot := filepath.Clean(filepath.Join(cwd, "..", ".."))

	// Stage the help-text source into the binary's package dir so Go's
	// //go:embed directive can reach it. The Makefile build target does
	// this; we replicate it here so `go test` works standalone.
	helpSrc := filepath.Join(repoRoot, "docs", "help", "meet-helper.txt")
	helpDst := filepath.Join(repoRoot, "cmd", "meet-helper", "help.txt")
	helpData, err := os.ReadFile(helpSrc)
	if err != nil {
		t.Fatalf("read help source %s: %v", helpSrc, err)
	}
	if err := os.WriteFile(helpDst, helpData, 0o644); err != nil {
		t.Fatalf("stage help.txt: %v", err)
	}

	out := filepath.Join(repoRoot, "bin", "meet-helper-test")
	cmd := exec.Command("go", "build", "-o", out, "./cmd/meet-helper")
	cmd.Dir = repoRoot
	if combined, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build meet-helper: %v\n%s", err, combined)
	}
	meetHelperBin = out
	return out
}

func runHelper(t *testing.T, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	return runHelperWithEnv(t, nil, args...)
}

func runHelperWithEnv(t *testing.T, env []string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	bin := ensureMeetHelperBin(t)
	cmd := exec.Command(bin, args...)
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	var sout, serr bytes.Buffer
	cmd.Stdout = &sout
	cmd.Stderr = &serr
	err := cmd.Run()
	exitCode = 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("exec meet-helper: %v", err)
	}
	return sout.String(), serr.String(), exitCode
}

func writeFakeMeet(t *testing.T, dir string) {
	t.Helper()
	script := `#!/usr/bin/env bash
case "$1" in
  serve|token|create|cancel|list)
    printf 'LOCAL_HELP:%s\n' "$1"
    ;;
  *)
    printf 'unknown command: %s\n' "$1" >&2
    exit 2
    ;;
esac
`
	path := filepath.Join(dir, "meet")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake meet: %v", err)
	}
}

func helperHelpSource(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoRoot := filepath.Clean(filepath.Join(cwd, "..", ".."))
	data, err := os.ReadFile(filepath.Join(repoRoot, "docs", "help", "meet-helper.txt"))
	if err != nil {
		t.Fatalf("read helper docs source: %v", err)
	}
	return string(data)
}

// AC10.1 — token subcommand: the constructed argv invokes the deploy-nix
// meet-admin wrapper on the remote host with the token subcommand and any
// forwarded args.
func TestHelper_TokenSubcommand_RT10_1(t *testing.T) {
	argv := sshshim.BuildSSHArgv("skys-edge", "token", []string{"--room", "foo"})
	if len(argv) != 2 {
		t.Fatalf("argv has %d elements, want 2 (host, remote-command)", len(argv))
	}
	if argv[0] != "skys-edge" {
		t.Errorf("argv[0] = %q, want %q", argv[0], "skys-edge")
	}
	if !strings.Contains(argv[1], "'sudo' '-u' 'meet' 'meet-admin'") {
		t.Errorf("remote command missing meet-admin wrapper: %q", argv[1])
	}
	if !strings.Contains(argv[1], "'token'") {
		t.Errorf("remote command missing token subcommand: %q", argv[1])
	}
	if !strings.Contains(argv[1], "'--room'") || !strings.Contains(argv[1], "'foo'") {
		t.Errorf("remote command missing --room foo: %q", argv[1])
	}
}

// AC10.2 — create subcommand with multiple args is forwarded in order.
func TestHelper_CreateSubcommand_RT10_3(t *testing.T) {
	argv := sshshim.BuildSSHArgv("skys-edge", "create", []string{
		"--room", "demo", "--from", "2026-05-25T19:00:00Z", "--until", "2026-05-25T21:00:00Z",
	})
	cmd := argv[1]
	// Confirm exact in-order appearance.
	expected := []string{"'create'", "'--room'", "'demo'", "'--from'", "'2026-05-25T19:00:00Z'", "'--until'", "'2026-05-25T21:00:00Z'"}
	pos := 0
	for _, e := range expected {
		i := strings.Index(cmd[pos:], e)
		if i < 0 {
			t.Errorf("remote command missing %q (in order): %q", e, cmd)
			return
		}
		pos += i + len(e)
	}
}

// AC10.2 — args containing shell metacharacters are quoted, not interpreted.
func TestHelper_ShellMetacharactersQuoted_RT10_4(t *testing.T) {
	cases := []struct {
		name string
		arg  string
	}{
		{"single quote", "it's"},
		{"semicolon", "foo;bar"},
		{"dollar sign", "$HOME"},
		{"backtick", "`whoami`"},
		{"spaces", "two words"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			argv := sshshim.BuildSSHArgv("h", "cancel", []string{"--note", c.arg})
			cmd := argv[1]
			// The note flag value must be enclosed in shell-quoting; it
			// must not appear as a bare token.
			rawMarker := " " + c.arg + " "
			if strings.Contains(" "+cmd+" ", rawMarker) {
				t.Errorf("arg %q appears unquoted in remote command: %q", c.arg, cmd)
			}
		})
	}
}

// AC10.1 — remote command goes through the NixOS meet-admin wrapper and does
// not embed config or secrets paths owned by deploy-nix.
func TestHelper_NixAdminWrapper_RT10_2(t *testing.T) {
	argv := sshshim.BuildSSHArgv("skys-edge", "list", nil)
	cmd := argv[1]
	if !strings.Contains(cmd, "'sudo' '-u' 'meet' 'meet-admin'") {
		t.Errorf("remote command missing NixOS admin wrapper: %q", cmd)
	}
	forbidden := []string{"--config", "/srv/meet/meet", "/srv/meet/repo/config", "/etc/meet/secrets.yaml", "/opt/apps/meet"}
	for _, phrase := range forbidden {
		if strings.Contains(cmd, phrase) {
			t.Errorf("remote command embeds %q instead of relying on meet-admin: %q", phrase, cmd)
		}
	}
}

// AC10.2 — hostname is the ssh destination only; app paths are supplied by
// the remote meet-admin wrapper.
func TestHelper_HostIsSSHDestinationOnly_RT10_5(t *testing.T) {
	for _, host := range []string{"light-hugger", "skys-edge", "chasm-city"} {
		t.Run(host, func(t *testing.T) {
			argv := sshshim.BuildSSHArgv(host, "list", nil)
			if strings.Contains(argv[1], fmt.Sprintf("%s.yaml", host)) {
				t.Errorf("remote command interpolates host into app config path; got: %q", argv[1])
			}
			if argv[0] != host {
				t.Errorf("ssh hostname = %q, want %q", argv[0], host)
			}
		})
	}
}

// AC8.3 — cmd/remote-token/ does not exist anywhere in the repo.
func TestHelper_OldDirectoryRemoved_RT8_6(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoRoot := filepath.Clean(filepath.Join(cwd, "..", ".."))
	if _, err := os.Stat(filepath.Join(repoRoot, "cmd", "remote-token")); !os.IsNotExist(err) {
		t.Errorf("cmd/remote-token/ still present (err=%v)", err)
	}
}

// AC8.3 — Makefile install target wires meet-helper, not meet-token.
func TestHelper_MakefileInstallsHelper_RT8_7(t *testing.T) {
	cwd, _ := os.Getwd()
	repoRoot := filepath.Clean(filepath.Join(cwd, "..", ".."))
	data, err := os.ReadFile(filepath.Join(repoRoot, "Makefile"))
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, "ln -sfn $(CURDIR)/bin/meet-helper $(HOME)/.local/bin/meet-helper") {
		t.Errorf("Makefile install target does not symlink meet-helper")
	}
	// Discriminate: the docs source file docs/help/meet-token.txt is a
	// legitimate reference to the `meet token` subcommand's prose; the
	// OLD binary references (bin/meet-token, install symlink, build
	// target on ./cmd/remote-token) are what should be gone.
	stripped := strings.ReplaceAll(body, "docs/help/meet-token.txt", "")
	if strings.Contains(stripped, "meet-token") || strings.Contains(stripped, "remote-token") {
		t.Errorf("Makefile still references the old meet-token / remote-token binary")
	}
}

// AC8.3 — the literal "meet-token" does not appear anywhere in tracked Go
// source, Markdown documentation, or the Makefile.
func TestHelper_NoMeetTokenReferences_RT8_8(t *testing.T) {
	cwd, _ := os.Getwd()
	repoRoot := filepath.Clean(filepath.Join(cwd, "..", ".."))
	suffixes := []string{".go", ".md", "Makefile"}
	skipDirs := map[string]bool{".git": true, ".claude": true, ".agent": true, "bin": true, "node_modules": true}

	var hits []string
	err := filepath.Walk(repoRoot, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			if skipDirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		match := false
		for _, sfx := range suffixes {
			if strings.HasSuffix(info.Name(), sfx) {
				match = true
				break
			}
		}
		if !match {
			return nil
		}
		// Don't scan the test file itself (it deliberately references the literal).
		if filepath.Base(path) == "meet_helper_test.go" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// Discriminate the OLD binary reference from legitimate
		// references to the per-subcommand docs file:
		//   docs/help/meet-token.txt  → docs source for `meet token`
		//   help-token.txt            → staged copy in cmd/meet/
		// Strip these before checking for the old-binary literal.
		content := string(data)
		content = strings.ReplaceAll(content, "docs/help/meet-token.txt", "")
		content = strings.ReplaceAll(content, "\"meet-token.txt\"", "")
		content = strings.ReplaceAll(content, "meet-token.txt", "")
		if strings.Contains(content, "meet-token") || strings.Contains(content, "remote-token") {
			hits = append(hits, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("meet-token / remote-token still referenced in: %v", hits)
	}
}

// AC8.4 — invocations with too few arguments exit non-zero.
func TestHelper_NoArgs_ExitsNonZero_RT8_9(t *testing.T) {
	docSrc := helperHelpSource(t)
	_, stderr, code := runHelper(t)
	if code == 0 {
		t.Errorf("no-args exit=0, want non-zero")
	}
	if stderr != docSrc {
		t.Errorf("stderr does not match docs/help/meet-helper.txt\n--- docs ---\n%s\n--- stderr ---\n%s", docSrc, stderr)
	}
}

// AC8.4 — -h and --help print local help and exit zero, whether they appear
// in position 0 (`meet-helper -h`) or position 1 (`meet-helper host -h`).
// Once a subcommand is named, the flag is forwarded to the remote — that
// path is not exercised here because it requires opening a real SSH
// connection.
func TestHelper_HelpFlags_ExitZero_RT8_10(t *testing.T) {
	docSrc := helperHelpSource(t)
	cases := [][]string{
		{"-h"},
		{"--help"},
		{"some-host", "-h"},
		{"some-host", "--help"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			stdout, _, code := runHelper(t, args...)
			if code != 0 {
				t.Errorf("%v: exit=%d, want 0", args, code)
			}
			if stdout != docSrc {
				t.Errorf("%v: stdout does not match docs/help/meet-helper.txt\n--- docs ---\n%s\n--- stdout ---\n%s", args, docSrc, stdout)
			}
		})
	}
}

// AC10.6 — after a subcommand has been named, --help is handled locally by
// the companion meet binary for every supported subcommand. This keeps help
// usable even when the remote NixOS admin wrapper needs config/secrets.
func TestHelper_SubcommandHelpHandledLocally_RT10_9(t *testing.T) {
	dir := t.TempDir()
	writeFakeMeet(t, dir)
	env := []string{"PATH=" + dir + string(os.PathListSeparator) + os.Getenv("PATH")}

	for _, subcommand := range []string{"serve", "token", "create", "cancel", "list"} {
		t.Run(subcommand, func(t *testing.T) {
			stdout, stderr, code := runHelperWithEnv(t, env, "skys-edge", subcommand, "--help")
			if code != 0 {
				t.Fatalf("%s help exit=%d, stderr=%q", subcommand, code, stderr)
			}
			if strings.TrimSpace(stdout) != "LOCAL_HELP:"+subcommand {
				t.Errorf("%s help did not come from local meet subcommand help: %q", subcommand, stdout)
			}
		})
	}
}

// AC8.4 — host alone without a subcommand exits non-zero.
func TestHelper_HostOnly_ExitsNonZero_RT8_11(t *testing.T) {
	_, _, code := runHelper(t, "some-host")
	if code == 0 {
		t.Errorf("host-only exit=0, want non-zero")
	}
}

// AC8.3 / centralised docs — the help text printed by meet-helper is the
// byte-for-byte content of docs/help/meet-helper.txt, not a hardcoded copy
// in Go source. Catches regressions where help drifts back into source.
func TestHelper_HelpTextSourcedFromDocs_RT8_12(t *testing.T) {
	docSrc := helperHelpSource(t)

	stdout, _, code := runHelper(t, "-h")
	if code != 0 {
		t.Fatalf("-h exit=%d", code)
	}
	if stdout != docSrc {
		t.Errorf("help output diverges from docs/help/meet-helper.txt\n--- docs ---\n%s\n--- output ---\n%s",
			docSrc, stdout)
	}
}
