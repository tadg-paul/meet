// ABOUTME: CLI tests for meet create / meet cancel / meet list (issue #7).
// ABOUTME: Builds the meet binary once via go build and execs it for each test
// ABOUTME: so the assertions ride the same user-facing entry point an operator
// ABOUTME: types into a terminal. Tests user-observable state: stdout, exit
// ABOUTME: code, and the resulting rooms.csv on disk.

package regression

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// meetBin is the path to the built meet binary, populated by TestMain.
var meetBin string

// TestMain builds the meet binary once for all CLI tests in this package.
// The build artefact lives in ./bin/ (gitignored) and is reused across tests.
func TestMain(m *testing.M) {
	bin, err := buildMeetBinary()
	if err != nil {
		fmt.Fprintf(os.Stderr, "build meet binary failed: %v\n", err)
		os.Exit(1)
	}
	meetBin = bin
	os.Exit(m.Run())
}

func buildMeetBinary() (string, error) {
	// Locate the repo root by walking up from this test file's directory
	// until we find go.mod. tests/regression is two levels deep.
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	repoRoot := filepath.Clean(filepath.Join(cwd, "..", ".."))

	// Stage docs/help/meet*.txt into cmd/meet/ so Go's //go:embed can
	// reach them. The Makefile build target does the same; we replicate
	// it here so `go test` works standalone (issue #9).
	stagings := map[string]string{
		"meet.txt":        "help-meet.txt",
		"meet-serve.txt":  "help-serve.txt",
		"meet-token.txt":  "help-token.txt",
		"meet-create.txt": "help-create.txt",
		"meet-cancel.txt": "help-cancel.txt",
		"meet-list.txt":   "help-list.txt",
	}
	for src, dst := range stagings {
		data, err := os.ReadFile(filepath.Join(repoRoot, "docs", "help", src))
		if err != nil {
			return "", fmt.Errorf("read docs help %s: %w", src, err)
		}
		if err := os.WriteFile(filepath.Join(repoRoot, "cmd", "meet", dst), data, 0o644); err != nil {
			return "", fmt.Errorf("stage %s: %w", dst, err)
		}
	}

	out := filepath.Join(repoRoot, "bin", "meet-cli-test")
	cmd := exec.Command("go", "build", "-o", out, "./cmd/meet")
	cmd.Dir = repoRoot
	if combined, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go build: %v\n%s", err, combined)
	}
	return out, nil
}

// runMeet execs the meet binary with the given args and a per-test
// STATE_DIRECTORY pointing at t.TempDir(). Returns stdout, stderr, exit code.
func runMeet(t *testing.T, stateDir string, args ...string) (stdout, stderr string, exitCode int) {
	t.Helper()
	cmd := exec.Command(meetBin, args...)
	cmd.Env = append(os.Environ(),
		"STATE_DIRECTORY="+stateDir,
		// Skip config file loading entirely - these tests don't need 8x8 keys,
		// they just exercise the registry CLI surface.
		"CONFIG_PATH=",
		"SECRETS_PATH=",
	)
	var sout, serr bytes.Buffer
	cmd.Stdout = &sout
	cmd.Stderr = &serr
	err := cmd.Run()
	exitCode = 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("exec meet: %v", err)
	}
	return sout.String(), serr.String(), exitCode
}

// readRoomsCSV returns the contents of rooms.csv in the given state dir, or
// the empty string if no such file exists.
func readRoomsCSV(t *testing.T, stateDir string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(stateDir, "rooms.csv"))
	if err != nil {
		if os.IsNotExist(err) {
			return ""
		}
		t.Fatalf("read rooms.csv: %v", err)
	}
	return string(data)
}

// AC7.3 — `meet create` writes a created row to rooms.csv and exits 0.
func TestCLI_Create_HappyPath_RT7_8(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := runMeet(t, dir,
		"create",
		"--room", "readers-2026-05-25",
		"--from", "2026-05-25T19:00:00Z",
		"--until", "2026-05-25T21:00:00Z",
		"--note", "book club",
	)
	if code != 0 {
		t.Fatalf("exit=%d, stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "created readers-2026-05-25") {
		t.Errorf("stdout does not announce creation: %q", stdout)
	}
	csv := readRoomsCSV(t, dir)
	if !strings.Contains(csv, "readers-2026-05-25,created,2026-05-25T19:00:00Z,2026-05-25T21:00:00Z,book club") {
		t.Errorf("rooms.csv missing expected row; got:\n%s", csv)
	}
}

// AC7.3 — `meet create` followed by an HTTP GET inside the window receives the
// meeting page. The HTTP half is covered by RT-7.4 (gate during-window) which
// runs against a server backed by the same rooms.csv contract; this RT
// verifies the CSV format the gate consumes.
func TestCLI_Create_StoredFormatRoundTrips_RT7_9(t *testing.T) {
	dir := t.TempDir()
	_, _, code := runMeet(t, dir,
		"create",
		"--room", "foo",
		"--from", "2026-05-25T19:00:00Z",
		"--until", "2026-05-25T21:00:00Z",
	)
	if code != 0 {
		t.Fatalf("create exit=%d", code)
	}
	csv := readRoomsCSV(t, dir)
	// Header must be canonical so the server can read it back.
	wantHeader := "timestamp,room,status,valid_from,valid_until,note\n"
	if !strings.HasPrefix(csv, wantHeader) {
		t.Errorf("rooms.csv header = %q, want %q", csv[:min(len(csv), 80)], wantHeader)
	}
	if !strings.Contains(csv, ",foo,created,2026-05-25T19:00:00Z,2026-05-25T21:00:00Z,") {
		t.Errorf("rooms.csv missing canonical row; got:\n%s", csv)
	}
}

// AC7.3 — `meet create` with --from later than --until exits non-zero and
// writes no row.
func TestCLI_Create_InvalidWindowRejected_RT7_10(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := runMeet(t, dir,
		"create",
		"--room", "bad",
		"--from", "2026-05-25T21:00:00Z",
		"--until", "2026-05-25T19:00:00Z",
	)
	if code == 0 {
		t.Errorf("inverted window: exit=0, want non-zero")
	}
	if !strings.Contains(stderr, "must be before") {
		t.Errorf("stderr does not explain the failure: %q", stderr)
	}
	csv := readRoomsCSV(t, dir)
	if strings.Contains(csv, "bad,created") {
		t.Errorf("rooms.csv contains a row for the rejected create; got:\n%s", csv)
	}
}

// AC7.3 — `meet create` with --room missing exits non-zero.
func TestCLI_Create_MissingRoomRejected_RT7_11(t *testing.T) {
	dir := t.TempDir()
	_, _, code := runMeet(t, dir,
		"create",
		"--from", "2026-05-25T19:00:00Z",
		"--until", "2026-05-25T21:00:00Z",
	)
	if code == 0 {
		t.Errorf("missing --room: exit=0, want non-zero")
	}
}

// AC7.4 — `meet cancel` on a registered room writes a cancelled row.
func TestCLI_Cancel_HappyPath_RT7_12(t *testing.T) {
	dir := t.TempDir()
	if _, _, code := runMeet(t, dir,
		"create", "--room", "foo",
		"--from", "2026-05-25T19:00:00Z",
		"--until", "2026-05-25T21:00:00Z",
	); code != 0 {
		t.Fatalf("create exit=%d", code)
	}
	stdout, stderr, code := runMeet(t, dir,
		"cancel", "--room", "foo", "--note", "postponed",
	)
	if code != 0 {
		t.Fatalf("cancel exit=%d, stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "cancelled foo") {
		t.Errorf("stdout does not announce cancellation: %q", stdout)
	}
	csv := readRoomsCSV(t, dir)
	if !strings.Contains(csv, ",foo,cancelled,") {
		t.Errorf("rooms.csv missing cancelled row; got:\n%s", csv)
	}
	if !strings.Contains(csv, "postponed") {
		t.Errorf("rooms.csv missing note; got:\n%s", csv)
	}
}

// AC7.4 — `meet cancel` on an unregistered room exits non-zero.
func TestCLI_Cancel_UnregisteredRejected_RT7_14(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := runMeet(t, dir, "cancel", "--room", "never-existed")
	if code == 0 {
		t.Errorf("cancel on unregistered: exit=0, want non-zero")
	}
	if !strings.Contains(stderr, "not registered") {
		t.Errorf("stderr does not explain the failure: %q", stderr)
	}
}

// AC7.5 — `meet list` (no filter) prints all rooms with their latest state.
func TestCLI_List_All_RT7_15(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a", "b", "c"} {
		if _, _, code := runMeet(t, dir,
			"create", "--room", name,
			"--from", "2026-05-25T19:00:00Z",
			"--until", "2026-05-25T21:00:00Z",
		); code != 0 {
			t.Fatalf("create %s exit=%d", name, code)
		}
	}
	stdout, _, code := runMeet(t, dir, "list")
	if code != 0 {
		t.Fatalf("list exit=%d", code)
	}
	for _, name := range []string{"a", "b", "c"} {
		if !strings.Contains(stdout, name) {
			t.Errorf("list output missing room %q; got:\n%s", name, stdout)
		}
	}
}

// AC7.5 — `meet list --filter cancelled` prints only cancelled rooms.
func TestCLI_List_FilterCancelled_RT7_16(t *testing.T) {
	dir := t.TempDir()
	if _, _, code := runMeet(t, dir,
		"create", "--room", "active-one",
		"--from", "2026-05-25T19:00:00Z",
		"--until", "2026-05-25T21:00:00Z",
	); code != 0 {
		t.Fatalf("create exit=%d", code)
	}
	if _, _, code := runMeet(t, dir,
		"create", "--room", "cancelled-one",
		"--from", "2026-05-25T19:00:00Z",
		"--until", "2026-05-25T21:00:00Z",
	); code != 0 {
		t.Fatalf("create exit=%d", code)
	}
	if _, _, code := runMeet(t, dir, "cancel", "--room", "cancelled-one"); code != 0 {
		t.Fatalf("cancel exit=%d", code)
	}
	stdout, _, code := runMeet(t, dir, "list", "--filter", "cancelled")
	if code != 0 {
		t.Fatalf("list exit=%d", code)
	}
	if !strings.Contains(stdout, "cancelled-one") {
		t.Errorf("filter=cancelled missing cancelled-one: %q", stdout)
	}
	if strings.Contains(stdout, "active-one") {
		t.Errorf("filter=cancelled included active-one: %q", stdout)
	}
}

// AC7.5 — `meet list --filter active` shows only rooms whose latest=created
// and whose now ∈ window. With wall-clock used by the running binary, we set
// the window to bracket now() with generous margins.
func TestCLI_List_FilterActive_RT7_17(t *testing.T) {
	dir := t.TempDir()
	// "active" — created with a window spanning the current real time
	if _, _, code := runMeet(t, dir,
		"create", "--room", "active-one",
		"--from", "2000-01-01T00:00:00Z",
		"--until", "2099-12-31T23:59:59Z",
	); code != 0 {
		t.Fatalf("create active exit=%d", code)
	}
	// "upcoming" — created but in the far future
	if _, _, code := runMeet(t, dir,
		"create", "--room", "upcoming-one",
		"--from", "2099-01-01T00:00:00Z",
		"--until", "2099-12-31T23:59:59Z",
	); code != 0 {
		t.Fatalf("create upcoming exit=%d", code)
	}
	stdout, _, code := runMeet(t, dir, "list", "--filter", "active")
	if code != 0 {
		t.Fatalf("list exit=%d", code)
	}
	if !strings.Contains(stdout, "active-one") {
		t.Errorf("filter=active missing active-one: %q", stdout)
	}
	if strings.Contains(stdout, "upcoming-one") {
		t.Errorf("filter=active included upcoming-one: %q", stdout)
	}
}

// AC7.5 — `meet list --filter past` shows only rooms whose latest=created
// and whose valid_until is in the past.
func TestCLI_List_FilterPast_RT7_18(t *testing.T) {
	dir := t.TempDir()
	if _, _, code := runMeet(t, dir,
		"create", "--room", "past-one",
		"--from", "2000-01-01T00:00:00Z",
		"--until", "2000-01-02T00:00:00Z",
	); code != 0 {
		t.Fatalf("create past exit=%d", code)
	}
	if _, _, code := runMeet(t, dir,
		"create", "--room", "active-one",
		"--from", "2000-01-01T00:00:00Z",
		"--until", "2099-12-31T23:59:59Z",
	); code != 0 {
		t.Fatalf("create active exit=%d", code)
	}
	stdout, _, code := runMeet(t, dir, "list", "--filter", "past")
	if code != 0 {
		t.Fatalf("list exit=%d", code)
	}
	if !strings.Contains(stdout, "past-one") {
		t.Errorf("filter=past missing past-one: %q", stdout)
	}
	if strings.Contains(stdout, "active-one") {
		t.Errorf("filter=past included active-one: %q", stdout)
	}
}

// AC7.7 — state survives across CLI invocations. A separate exec of meet
// over the same STATE_DIRECTORY reads the previously-created room.
func TestCLI_Persistence_RegisteredSurvivesAcrossExec_RT7_23(t *testing.T) {
	dir := t.TempDir()
	if _, _, code := runMeet(t, dir,
		"create", "--room", "persist-me",
		"--from", "2026-05-25T19:00:00Z",
		"--until", "2026-05-25T21:00:00Z",
		"--note", "survives restart",
	); code != 0 {
		t.Fatalf("create exit=%d", code)
	}
	// Fresh exec — simulates restart.
	stdout, _, code := runMeet(t, dir, "list")
	if code != 0 {
		t.Fatalf("list exit=%d", code)
	}
	if !strings.Contains(stdout, "persist-me") {
		t.Errorf("second exec did not see persist-me: %q", stdout)
	}
	if !strings.Contains(stdout, "survives restart") {
		t.Errorf("second exec did not see the note: %q", stdout)
	}
}

// AC7.7 — cancellation also survives across CLI invocations.
func TestCLI_Persistence_CancellationSurvivesAcrossExec_RT7_24(t *testing.T) {
	dir := t.TempDir()
	if _, _, code := runMeet(t, dir,
		"create", "--room", "foo",
		"--from", "2026-05-25T19:00:00Z",
		"--until", "2026-05-25T21:00:00Z",
	); code != 0 {
		t.Fatalf("create exit=%d", code)
	}
	if _, _, code := runMeet(t, dir, "cancel", "--room", "foo"); code != 0 {
		t.Fatalf("cancel exit=%d", code)
	}
	stdout, _, code := runMeet(t, dir, "list", "--filter", "cancelled")
	if code != 0 {
		t.Fatalf("list exit=%d", code)
	}
	if !strings.Contains(stdout, "foo") {
		t.Errorf("filter=cancelled did not list foo after fresh exec: %q", stdout)
	}
}
