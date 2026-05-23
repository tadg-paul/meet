// ABOUTME: Regression tests for the meet-helper SSH shim (issue #8).
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

	"github.com/tadg-paul/meet/internal/sshshim"
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
	bin := ensureMeetHelperBin(t)
	cmd := exec.Command(bin, args...)
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

// AC8.1 — token subcommand: the constructed argv invokes /srv/meet/meet
// on the remote host with the token subcommand and any forwarded args.
func TestHelper_TokenSubcommand_RT8_1(t *testing.T) {
	argv := sshshim.BuildSSHArgv("light-hugger", "token", []string{"--room", "foo"})
	if len(argv) != 2 {
		t.Fatalf("argv has %d elements, want 2 (host, remote-command)", len(argv))
	}
	if argv[0] != "light-hugger" {
		t.Errorf("argv[0] = %q, want %q", argv[0], "light-hugger")
	}
	if !strings.Contains(argv[1], "'/srv/meet/meet'") {
		t.Errorf("remote command missing meet binary: %q", argv[1])
	}
	if !strings.Contains(argv[1], "'token'") {
		t.Errorf("remote command missing token subcommand: %q", argv[1])
	}
	if !strings.Contains(argv[1], "'--room'") || !strings.Contains(argv[1], "'foo'") {
		t.Errorf("remote command missing --room foo: %q", argv[1])
	}
}

// AC8.1 — create subcommand with multiple args is forwarded in order.
func TestHelper_CreateSubcommand_RT8_2(t *testing.T) {
	argv := sshshim.BuildSSHArgv("light-hugger", "create", []string{
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

// AC8.1 — args containing shell metacharacters are quoted, not interpreted.
func TestHelper_ShellMetacharactersQuoted_RT8_3(t *testing.T) {
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

// AC8.2 — --config flag value contains exactly the canonical cascade.
func TestHelper_ConfigCascade_RT8_4(t *testing.T) {
	argv := sshshim.BuildSSHArgv("light-hugger", "list", nil)
	cmd := argv[1]
	wantCascade := "/srv/meet/repo/config/defaults.yaml,/srv/meet/repo/config/light-hugger.yaml,/etc/meet/secrets.yaml"
	if !strings.Contains(cmd, wantCascade) {
		t.Errorf("remote command missing canonical cascade %q; got: %q", wantCascade, cmd)
	}
	// Make sure --config appears exactly once.
	if strings.Count(cmd, "'--config'") != 1 {
		t.Errorf("expected exactly one --config flag in remote command; got: %q", cmd)
	}
}

// AC8.2 — hostname is interpolated verbatim into the per-host config path.
func TestHelper_HostInterpolation_RT8_5(t *testing.T) {
	for _, host := range []string{"light-hugger", "skys-edge", "chasm-city"} {
		t.Run(host, func(t *testing.T) {
			argv := sshshim.BuildSSHArgv(host, "list", nil)
			want := fmt.Sprintf("/srv/meet/repo/config/%s.yaml", host)
			if !strings.Contains(argv[1], want) {
				t.Errorf("remote command missing host-specific config %q; got: %q", want, argv[1])
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
	if strings.Contains(body, "meet-token") {
		t.Errorf("Makefile still references meet-token")
	}
}

// AC8.3 — the literal "meet-token" does not appear anywhere in tracked Go
// source, Markdown documentation, or the Makefile.
func TestHelper_NoMeetTokenReferences_RT8_8(t *testing.T) {
	cwd, _ := os.Getwd()
	repoRoot := filepath.Clean(filepath.Join(cwd, "..", ".."))
	suffixes := []string{".go", ".md", "Makefile"}
	skipDirs := map[string]bool{".git": true, "bin": true, "node_modules": true}

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
		if strings.Contains(string(data), "meet-token") || strings.Contains(string(data), "remote-token") {
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
	_, stderr, code := runHelper(t)
	if code == 0 {
		t.Errorf("no-args exit=0, want non-zero")
	}
	if !strings.Contains(stderr, "Usage:") {
		t.Errorf("stderr missing usage line: %q", stderr)
	}
}

// AC8.4 — -h and --help print help and exit zero.
func TestHelper_HelpFlags_ExitZero_RT8_10(t *testing.T) {
	for _, flag := range []string{"-h", "--help"} {
		t.Run(flag, func(t *testing.T) {
			stdout, _, code := runHelper(t, flag)
			if code != 0 {
				t.Errorf("%s: exit=%d, want 0", flag, code)
			}
			if !strings.Contains(stdout, "Usage:") {
				t.Errorf("%s: stdout missing usage line: %q", flag, stdout)
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
