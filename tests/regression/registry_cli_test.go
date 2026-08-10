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
	// Header must be canonical so the server can read it back. The recurrence
	// columns were appended by #17; one-off rows leave them empty.
	wantHeader := "timestamp,room,status,valid_from,valid_until,note," +
		"recur_kind,recur_interval,recur_ordinal,recur_weekday," +
		"recur_duration_s,recur_lead_s,recur_ends,recur_tz\n"
	if !strings.HasPrefix(csv, wantHeader) {
		t.Errorf("rooms.csv header = %q, want %q", csv[:min(len(csv), 120)], wantHeader)
	}
	if !strings.Contains(csv, ",foo,created,2026-05-25T19:00:00Z,2026-05-25T21:00:00Z,") {
		t.Errorf("rooms.csv missing canonical row; got:\n%s", csv)
	}
}

// AC10.5 — `meet create --from/--until` accepts date-only input and stores it
// as midnight UTC for each date.
func TestCLI_Create_DateOnlyFromUntil_RT10_6(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := runMeet(t, dir,
		"create",
		"--room", "date-only",
		"--from", "2026-06-09",
		"--until", "2026-06-10",
	)
	if code != 0 {
		t.Fatalf("exit=%d, stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "created date-only [2026-06-09T00:00:00Z .. 2026-06-10T00:00:00Z]") {
		t.Errorf("stdout does not show normalized dates: %q", stdout)
	}
	csv := readRoomsCSV(t, dir)
	if !strings.Contains(csv, ",date-only,created,2026-06-09T00:00:00Z,2026-06-10T00:00:00Z,") {
		t.Errorf("rooms.csv missing normalized date-only row; got:\n%s", csv)
	}
}

// AC10.5 — `meet create --on` registers a whole UTC day without requiring
// explicit from/until timestamps.
func TestCLI_Create_OnDate_RT10_7(t *testing.T) {
	dir := t.TempDir()
	stdout, stderr, code := runMeet(t, dir,
		"create",
		"--room", "all-day",
		"--on", "2026-06-09",
	)
	if code != 0 {
		t.Fatalf("exit=%d, stderr=%q", code, stderr)
	}
	if !strings.Contains(stdout, "created all-day [2026-06-09T00:00:00Z .. 2026-06-09T23:59:59Z]") {
		t.Errorf("stdout does not show all-day range: %q", stdout)
	}
	csv := readRoomsCSV(t, dir)
	if !strings.Contains(csv, ",all-day,created,2026-06-09T00:00:00Z,2026-06-09T23:59:59Z,") {
		t.Errorf("rooms.csv missing all-day row; got:\n%s", csv)
	}
}

// AC10.5 — `--on` is a shortcut, not an extra bound layered on top of
// explicit from/until values.
func TestCLI_Create_OnDateRejectsExplicitBounds_RT10_8(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := runMeet(t, dir,
		"create",
		"--room", "mixed",
		"--on", "2026-06-09",
		"--from", "2026-06-09T12:00:00Z",
		"--until", "2026-06-09T13:00:00Z",
	)
	if code == 0 {
		t.Errorf("mixed --on/--from/--until: exit=0, want non-zero")
	}
	if !strings.Contains(stderr, "--on cannot be combined") {
		t.Errorf("stderr does not explain the failure: %q", stderr)
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

// --- #17: recurrence create, config defaults, validation ---

func writeConfigFile(t *testing.T, dir, content string) string {
	t.Helper()
	p := filepath.Join(dir, "cfg.yaml")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return p
}

// AC17.8 / RT-17.26 — create --repeat weekly writes a recurring row.
func TestCLI_Create_Weekly_RT17_26(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := runMeet(t, dir, "create", "--room", "wk",
		"--repeat", "weekly", "--from", "2026-08-11T19:00:00Z")
	if code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr)
	}
	if csv := readRoomsCSV(t, dir); !strings.Contains(csv, ",weekly,1,0,0,14400,900,") {
		t.Errorf("rooms.csv missing weekly recurrence row; got:\n%s", csv)
	}
}

// AC17.8 / RT-17.27 — create --repeat monthly writes a recurring row.
func TestCLI_Create_Monthly_RT17_27(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := runMeet(t, dir, "create", "--room", "mo",
		"--repeat", "monthly", "--ordinal", "1", "--weekday", "wed", "--at", "18:00")
	if code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr)
	}
	if csv := readRoomsCSV(t, dir); !strings.Contains(csv, ",monthly,0,1,3,14400,900,") {
		t.Errorf("rooms.csv missing monthly recurrence row; got:\n%s", csv)
	}
}

// AC17.8 / RT-17.28 — an unknown weekday is rejected and writes no row.
func TestCLI_Create_BadWeekday_RT17_28(t *testing.T) {
	dir := t.TempDir()
	_, _, code := runMeet(t, dir, "create", "--room", "mo",
		"--repeat", "monthly", "--ordinal", "1", "--weekday", "funday", "--at", "18:00")
	if code == 0 {
		t.Error("bad weekday should exit non-zero")
	}
	if strings.Contains(readRoomsCSV(t, dir), "mo,created") {
		t.Error("bad weekday should write no row")
	}
}

// AC17.8 / RT-17.29 — an out-of-range ordinal is rejected and writes no row.
func TestCLI_Create_BadOrdinal_RT17_29(t *testing.T) {
	dir := t.TempDir()
	_, _, code := runMeet(t, dir, "create", "--room", "mo",
		"--repeat", "monthly", "--ordinal", "9", "--weekday", "wed", "--at", "18:00")
	if code == 0 {
		t.Error("bad ordinal should exit non-zero")
	}
	if strings.Contains(readRoomsCSV(t, dir), "mo,created") {
		t.Error("bad ordinal should write no row")
	}
}

// AC17.8 / RT-17.32 — --ends records the series end on the row.
func TestCLI_Create_Ends_RT17_32(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := runMeet(t, dir, "create", "--room", "wk",
		"--repeat", "weekly", "--from", "2026-08-11T19:00:00Z", "--ends", "2026-12-31")
	if code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr)
	}
	if csv := readRoomsCSV(t, dir); !strings.Contains(csv, "2026-12-31T23:59:59Z") {
		t.Errorf("rooms.csv missing series end; got:\n%s", csv)
	}
}

// AC17.8 / RT-17.33 — the deprecated --until still records a one-off window.
func TestCLI_Create_DeprecatedUntil_RT17_33(t *testing.T) {
	dir := t.TempDir()
	_, stderr, code := runMeet(t, dir, "create", "--room", "one",
		"--from", "2026-08-11T19:00:00Z", "--until", "2026-08-11T21:00:00Z")
	if code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr)
	}
	if csv := readRoomsCSV(t, dir); !strings.Contains(csv, "one,created,2026-08-11T19:00:00Z,2026-08-11T21:00:00Z,") {
		t.Errorf("rooms.csv missing one-off row from --until; got:\n%s", csv)
	}
}

// AC17.4 / RT-17.13, RT-17.14 — config defaults for duration and lead are
// recorded on the row when the flags are omitted.
func TestCLI_Create_ConfigDefaults_RT17_13_RT17_14(t *testing.T) {
	dir := t.TempDir()
	cfg := writeConfigFile(t, dir, "meeting:\n  default-duration: 3h\n  default-open-early: 20m\n")
	_, stderr, code := runMeet(t, dir, "create", "--config", cfg, "--room", "wk",
		"--repeat", "weekly", "--from", "2026-08-11T19:00:00Z")
	if code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr)
	}
	// 3h = 10800s, 20m = 1200s
	if csv := readRoomsCSV(t, dir); !strings.Contains(csv, ",weekly,1,0,0,10800,1200,") {
		t.Errorf("config defaults not applied; got:\n%s", csv)
	}
}

// AC17.4 / RT-17.15, RT-17.16 — explicit flags override the config defaults.
func TestCLI_Create_OverrideDefaults_RT17_15_RT17_16(t *testing.T) {
	dir := t.TempDir()
	cfg := writeConfigFile(t, dir, "meeting:\n  default-duration: 3h\n  default-open-early: 20m\n")
	_, stderr, code := runMeet(t, dir, "create", "--config", cfg, "--room", "wk",
		"--repeat", "weekly", "--from", "2026-08-11T19:00:00Z",
		"--duration", "2h", "--open-early", "5m")
	if code != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr)
	}
	// 2h = 7200s, 5m = 300s
	if csv := readRoomsCSV(t, dir); !strings.Contains(csv, ",weekly,1,0,0,7200,300,") {
		t.Errorf("explicit flags did not override config; got:\n%s", csv)
	}
}
