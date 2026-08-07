// ABOUTME: Regression tests for the shared per-room meeting timer (issue #15).
// ABOUTME: Exercises the timer HTTP surface (state, control, SSE, persistence)
// ABOUTME: through httptest with an injected clock; covers AC15.1-AC15.9, AC15.16.

package regression

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/tigger-developer/meet/internal/server"
)

// --- state view (decoded from the timer JSON) ---

type timerConfigView struct {
	Total        int `json:"total"`
	WarnPercent  int `json:"warnPercent"`
	GracePercent int `json:"gracePercent"`
	WarnSeconds  int `json:"warnSeconds"`
	GraceSeconds int `json:"graceSeconds"`
}

type stateView struct {
	Phase     string          `json:"phase"`
	Elapsed   int             `json:"elapsed"`
	Remaining int             `json:"remaining"`
	CountUp   int             `json:"countUp"`
	Running   bool            `json:"running"`
	Paused    bool            `json:"paused"`
	Extended  bool            `json:"extended"`
	Config    timerConfigView `json:"config"`
}

// --- fixture ---

type timerFixture struct {
	ts      *httptest.Server
	dataDir string
	privKey *rsa.PrivateKey
	now     time.Time
}

func newTimerFixture(t *testing.T) *timerFixture {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	f := &timerFixture{
		dataDir: t.TempDir(),
		privKey: priv,
		now:     mustRFC3339(t, "2026-05-22T20:00:00Z"),
	}
	f.ts = f.build(t)
	t.Cleanup(func() {
		if f.ts != nil {
			f.ts.Close()
		}
	})
	return f
}

func (f *timerFixture) build(t *testing.T) *httptest.Server {
	t.Helper()
	srv := server.New(server.Config{
		Addr:         "127.0.0.1:0",
		BaseURL:      "https://meet.lobb.ie",
		AppID:        "vpaas-magic-cookie-test",
		DataDir:      f.dataDir,
		Logger:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		JWTPublicKey: &f.privKey.PublicKey,
		Now:          func() time.Time { return f.now },
	})
	return httptest.NewServer(srv.Handler())
}

// restart simulates a server restart against the same state directory.
func (f *timerFixture) restart(t *testing.T) {
	t.Helper()
	f.ts.Close()
	f.ts = f.build(t)
}

func (f *timerFixture) advance(d time.Duration) { f.now = f.now.Add(d) }

func (f *timerFixture) jwt(t *testing.T, room string) string {
	t.Helper()
	now := time.Now()
	claims := jwt.MapClaims{
		"aud":  "jitsi",
		"iss":  "chat",
		"sub":  "vpaas-magic-cookie-test",
		"room": room,
		"iat":  now.Unix(),
		"nbf":  now.Add(-time.Minute).Unix(),
		"exp":  now.Add(2 * time.Hour).Unix(),
		"context": map[string]any{
			"user": map[string]any{"name": "Moderator", "moderator": "true"},
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := tok.SignedString(f.privKey)
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	return signed
}

// control POSTs an action, authenticated with the given jwt query value (may be
// empty for the no-token case). Returns the HTTP status and, when 200, the state.
func (f *timerFixture) control(t *testing.T, room, action string, config map[string]any, jwt string) (int, stateView) {
	t.Helper()
	body := map[string]any{"action": action}
	if config != nil {
		body["config"] = config
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	u := f.ts.URL + "/" + room + "/timer"
	if jwt != "" {
		u += "?jwt=" + url.QueryEscape(jwt)
	}
	resp, err := http.Post(u, "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("POST %s: %v", action, err)
	}
	defer resp.Body.Close()
	var sv stateView
	if resp.StatusCode == http.StatusOK {
		if err := json.NewDecoder(resp.Body).Decode(&sv); err != nil {
			t.Fatalf("decode control state: %v", err)
		}
	}
	return resp.StatusCode, sv
}

// modControl is control with a valid room-scoped moderator token.
func (f *timerFixture) modControl(t *testing.T, room, action string, config map[string]any) stateView {
	t.Helper()
	status, sv := f.control(t, room, action, config, f.jwt(t, room))
	if status != http.StatusOK {
		t.Fatalf("%s on %s: status %d, want 200", action, room, status)
	}
	return sv
}

func (f *timerFixture) state(t *testing.T, room string) stateView {
	t.Helper()
	resp, err := http.Get(f.ts.URL + "/" + room + "/timer")
	if err != nil {
		t.Fatalf("GET timer state: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET timer state: status %d", resp.StatusCode)
	}
	var sv stateView
	if err := json.NewDecoder(resp.Body).Decode(&sv); err != nil {
		t.Fatalf("decode state: %v", err)
	}
	return sv
}

// --- SSE stream helper ---

type sseStream struct {
	resp   *http.Response
	events chan stateView
	errs   chan error
}

func (f *timerFixture) openSSE(t *testing.T, room string) *sseStream {
	t.Helper()
	resp, err := http.Get(f.ts.URL + "/" + room + "/timer/events")
	if err != nil {
		t.Fatalf("open SSE: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("open SSE: status %d", resp.StatusCode)
	}
	s := &sseStream{resp: resp, events: make(chan stateView, 32), errs: make(chan error, 1)}
	go func() {
		r := bufio.NewReader(resp.Body)
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				s.errs <- err
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			var sv stateView
			if json.Unmarshal([]byte(strings.TrimSpace(line[len("data:"):])), &sv) == nil {
				s.events <- sv
			}
		}
	}()
	t.Cleanup(func() { resp.Body.Close() })
	return s
}

func (s *sseStream) next(t *testing.T) stateView {
	t.Helper()
	select {
	case sv := <-s.events:
		return sv
	case err := <-s.errs:
		t.Fatalf("SSE stream error: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("SSE stream: timed out waiting for event")
	}
	return stateView{}
}

// stdConfig is a 5:00 timer, 20% warning (warnAt=240s), 30% grace (limit=90s).
func stdConfig() map[string]any {
	return map[string]any{"total": 300, "warnPercent": 20, "gracePercent": 30}
}

// --- AC15.1: configuration and derived thresholds ---

func TestTimer_WarnThreshold_RT15_1(t *testing.T) {
	f := newTimerFixture(t)
	sv := f.modControl(t, "r", "set", map[string]any{"total": 300, "warnPercent": 10, "gracePercent": 30})
	if sv.Config.WarnSeconds != 30 {
		t.Errorf("warnSeconds = %d, want 30 (10%% of 300)", sv.Config.WarnSeconds)
	}
}

func TestTimer_GraceLimit_RT15_2(t *testing.T) {
	f := newTimerFixture(t)
	sv := f.modControl(t, "r", "set", map[string]any{"total": 900, "warnPercent": 20, "gracePercent": 30})
	if sv.Config.GraceSeconds != 270 {
		t.Errorf("graceSeconds = %d, want 270 (30%% of 900)", sv.Config.GraceSeconds)
	}
}

func TestTimer_PercentRounding_RT15_3(t *testing.T) {
	f := newTimerFixture(t)
	// 33% of 30 = 9.9 -> rounds to 10.
	sv := f.modControl(t, "r", "set", map[string]any{"total": 30, "warnPercent": 10, "gracePercent": 33})
	if sv.Config.GraceSeconds != 10 {
		t.Errorf("graceSeconds = %d, want 10 (round 9.9)", sv.Config.GraceSeconds)
	}
}

func TestTimer_RejectNonPositiveTotal_RT15_4(t *testing.T) {
	f := newTimerFixture(t)
	status, _ := f.control(t, "r", "set", map[string]any{"total": 0, "warnPercent": 20, "gracePercent": 30}, f.jwt(t, "r"))
	if status == http.StatusOK {
		t.Errorf("set with total 0 returned %d, want non-200", status)
	}
	if got := f.state(t, "r").Config.Total; got != 900 {
		t.Errorf("after rejected set, total = %d, want default 900 (nothing stored)", got)
	}
}

func TestTimer_RejectOutOfRangePercent_RT15_5(t *testing.T) {
	f := newTimerFixture(t)
	for _, cfg := range []map[string]any{
		{"total": 300, "warnPercent": 150, "gracePercent": 30},
		{"total": 300, "warnPercent": 20, "gracePercent": -5},
	} {
		status, _ := f.control(t, "r", "set", cfg, f.jwt(t, "r"))
		if status == http.StatusOK {
			t.Errorf("set %v returned %d, want non-200", cfg, status)
		}
	}
	if got := f.state(t, "r").Config.Total; got != 900 {
		t.Errorf("after rejected sets, total = %d, want default 900", got)
	}
}

// --- AC15.2: phases as a function of elapsed run time ---

func startStd(t *testing.T, f *timerFixture, room string) {
	t.Helper()
	f.modControl(t, room, "set", stdConfig())
	f.modControl(t, room, "start", nil)
}

func TestTimer_BeforeWarning_RT15_6(t *testing.T) {
	f := newTimerFixture(t)
	startStd(t, f, "r")
	f.advance(239 * time.Second) // warnAt = 240
	if sv := f.state(t, "r"); sv.Phase != "before-warning" {
		t.Errorf("phase at 239s = %q, want before-warning", sv.Phase)
	}
}

func TestTimer_AfterWarning_RT15_7(t *testing.T) {
	f := newTimerFixture(t)
	startStd(t, f, "r")
	f.advance(240 * time.Second) // inclusive at warnAt
	if sv := f.state(t, "r"); sv.Phase != "after-warning" {
		t.Errorf("phase at 240s = %q, want after-warning", sv.Phase)
	}
}

func TestTimer_GraceStart_RT15_8(t *testing.T) {
	f := newTimerFixture(t)
	startStd(t, f, "r")
	f.advance(300 * time.Second) // == total
	sv := f.state(t, "r")
	if sv.Phase != "grace" || sv.CountUp != 0 {
		t.Errorf("phase at total = %q countUp=%d, want grace countUp=0", sv.Phase, sv.CountUp)
	}
}

func TestTimer_WithinGrace_RT15_9(t *testing.T) {
	f := newTimerFixture(t)
	startStd(t, f, "r")
	f.advance(350 * time.Second) // total+50
	sv := f.state(t, "r")
	if sv.Phase != "grace" || sv.CountUp != 50 {
		t.Errorf("phase=%q countUp=%d, want grace countUp=50", sv.Phase, sv.CountUp)
	}
}

func TestTimer_GraceExceeded_RT15_10(t *testing.T) {
	f := newTimerFixture(t)
	startStd(t, f, "r")
	f.advance(390 * time.Second) // total+graceLimit = 300+90
	if sv := f.state(t, "r"); sv.Phase != "exceeded" {
		t.Errorf("phase at grace limit = %q, want exceeded", sv.Phase)
	}
}

func TestTimer_RemainingTracksClock_RT15_11(t *testing.T) {
	f := newTimerFixture(t)
	startStd(t, f, "r")
	f.advance(100 * time.Second)
	if sv := f.state(t, "r"); sv.Remaining != 200 {
		t.Errorf("remaining at 100s = %d, want 200", sv.Remaining)
	}
}

// --- AC15.3: pause freezes elapsed, resume continues ---

func TestTimer_PauseFreezes_RT15_12(t *testing.T) {
	f := newTimerFixture(t)
	startStd(t, f, "r")
	f.advance(100 * time.Second)
	f.modControl(t, "r", "pause", nil)
	f.advance(100 * time.Second) // wall advances, run-elapsed must not
	sv := f.state(t, "r")
	if sv.Elapsed != 100 || sv.Remaining != 200 || sv.Phase != "before-warning" {
		t.Errorf("paused: elapsed=%d remaining=%d phase=%q, want 100/200/before-warning", sv.Elapsed, sv.Remaining, sv.Phase)
	}
}

func TestTimer_ResumeContinues_RT15_13(t *testing.T) {
	f := newTimerFixture(t)
	startStd(t, f, "r")
	f.advance(100 * time.Second)
	f.modControl(t, "r", "pause", nil)
	f.advance(100 * time.Second)
	f.modControl(t, "r", "resume", nil)
	f.advance(150 * time.Second) // elapsed now 250 -> after-warning
	if sv := f.state(t, "r"); sv.Phase != "after-warning" {
		t.Errorf("after resume+150s phase=%q, want after-warning", sv.Phase)
	}
}

func TestTimer_PauseHoldsAfterWarning_RT15_14(t *testing.T) {
	f := newTimerFixture(t)
	startStd(t, f, "r")
	f.advance(250 * time.Second) // after-warning
	f.modControl(t, "r", "pause", nil)
	f.advance(100 * time.Second)
	if sv := f.state(t, "r"); sv.Phase != "after-warning" {
		t.Errorf("paused in after-warning, phase=%q after wall advance, want after-warning", sv.Phase)
	}
}

// --- AC15.4: reset, restart, stop ---

func TestTimer_ResetRetainsConfig_RT15_15(t *testing.T) {
	f := newTimerFixture(t)
	startStd(t, f, "r")
	f.advance(50 * time.Second)
	if sv := f.modControl(t, "r", "reset", nil); sv.Phase != "stopped" {
		t.Errorf("after reset phase=%q, want stopped", sv.Phase)
	}
	sv := f.modControl(t, "r", "start", nil)
	if sv.Config.Total != 300 {
		t.Errorf("after reset+start total=%d, want retained 300", sv.Config.Total)
	}
}

func TestTimer_RestartFromZero_RT15_16(t *testing.T) {
	f := newTimerFixture(t)
	startStd(t, f, "r")
	f.advance(100 * time.Second)
	sv := f.modControl(t, "r", "restart", nil)
	if sv.Elapsed != 0 || !sv.Running || sv.Phase != "before-warning" {
		t.Errorf("restart: elapsed=%d running=%v phase=%q, want 0/true/before-warning", sv.Elapsed, sv.Running, sv.Phase)
	}
}

func TestTimer_StopFromGrace_RT15_17(t *testing.T) {
	f := newTimerFixture(t)
	startStd(t, f, "r")
	f.advance(310 * time.Second) // grace
	if sv := f.modControl(t, "r", "stop", nil); sv.Phase != "stopped" {
		t.Errorf("stop from grace phase=%q, want stopped", sv.Phase)
	}
}

// --- AC15.5: grace exceeded and extend ---

func TestTimer_GraceExceededAutoStops_RT15_18(t *testing.T) {
	f := newTimerFixture(t)
	startStd(t, f, "r")
	f.advance(391 * time.Second) // just past grace limit (390)
	if sv := f.state(t, "r"); sv.Phase != "exceeded" {
		t.Errorf("just past grace limit phase=%q, want exceeded", sv.Phase)
	}
	f.advance(10 * time.Second) // past the 10s flash window (>=400)
	if sv := f.state(t, "r"); sv.Phase != "stopped" {
		t.Errorf("after flash window phase=%q, want stopped", sv.Phase)
	}
}

func TestTimer_ExtendContinuesPastLimit_RT15_19(t *testing.T) {
	f := newTimerFixture(t)
	startStd(t, f, "r")
	f.advance(350 * time.Second) // in grace
	f.modControl(t, "r", "extend", nil)
	f.advance(150 * time.Second) // elapsed 500, well past grace limit + flash
	sv := f.state(t, "r")
	if sv.Phase != "grace" || !sv.Extended || sv.CountUp != 200 {
		t.Errorf("extended: phase=%q extended=%v countUp=%d, want grace/true/200", sv.Phase, sv.Extended, sv.CountUp)
	}
}

func TestTimer_StopAfterExtend_RT15_20(t *testing.T) {
	f := newTimerFixture(t)
	startStd(t, f, "r")
	f.advance(350 * time.Second)
	f.modControl(t, "r", "extend", nil)
	f.advance(100 * time.Second)
	if sv := f.modControl(t, "r", "stop", nil); sv.Phase != "stopped" {
		t.Errorf("stop after extend phase=%q, want stopped", sv.Phase)
	}
}

// --- AC15.6: moderator-only, room-scoped control ---

func TestTimer_NoToken_Rejected_RT15_21(t *testing.T) {
	f := newTimerFixture(t)
	if status, _ := f.control(t, "r", "start", nil, ""); status == http.StatusOK {
		t.Errorf("start with no token returned %d, want non-200", status)
	}
	if sv := f.state(t, "r"); sv.Phase != "stopped" {
		t.Errorf("state after rejected start = %q, want stopped (unchanged)", sv.Phase)
	}
}

func TestTimer_InvalidToken_Rejected_RT15_22(t *testing.T) {
	f := newTimerFixture(t)
	if status, _ := f.control(t, "r", "start", nil, "not.a.jwt"); status == http.StatusOK {
		t.Errorf("start with invalid token returned %d, want non-200", status)
	}
	if sv := f.state(t, "r"); sv.Phase != "stopped" {
		t.Errorf("state after invalid-token start = %q, want stopped", sv.Phase)
	}
}

func TestTimer_WrongRoomToken_Rejected_RT15_23(t *testing.T) {
	f := newTimerFixture(t)
	if status, _ := f.control(t, "foo", "start", nil, f.jwt(t, "other")); status == http.StatusOK {
		t.Errorf("start with other-room token returned %d, want non-200", status)
	}
	if sv := f.state(t, "foo"); sv.Phase != "stopped" {
		t.Errorf("state after wrong-room start = %q, want stopped", sv.Phase)
	}
}

func TestTimer_RoomScopedToken_Applied_RT15_24(t *testing.T) {
	f := newTimerFixture(t)
	status, sv := f.control(t, "foo", "start", nil, f.jwt(t, "foo"))
	if status != http.StatusOK || !sv.Running {
		t.Errorf("room-scoped start: status=%d running=%v, want 200/true", status, sv.Running)
	}
}

func TestTimer_WildcardToken_Applied_RT15_25(t *testing.T) {
	f := newTimerFixture(t)
	status, sv := f.control(t, "foo", "start", nil, f.jwt(t, "*"))
	if status != http.StatusOK || !sv.Running {
		t.Errorf("wildcard start: status=%d running=%v, want 200/true", status, sv.Running)
	}
}

// --- AC15.7: subscription carries current and subsequent state ---

func TestTimer_SubscriptionCurrentState_RT15_26(t *testing.T) {
	f := newTimerFixture(t)
	s := f.openSSE(t, "r")
	if sv := s.next(t); sv.Phase != "stopped" {
		t.Errorf("first SSE event phase=%q, want stopped", sv.Phase)
	}
}

func TestTimer_SubscriptionLateJoin_RT15_27(t *testing.T) {
	f := newTimerFixture(t)
	startStd(t, f, "r")
	f.advance(100 * time.Second)
	s := f.openSSE(t, "r")
	sv := s.next(t)
	if sv.Phase != "before-warning" || sv.Elapsed != 100 {
		t.Errorf("late-join first event phase=%q elapsed=%d, want before-warning/100", sv.Phase, sv.Elapsed)
	}
}

func TestTimer_SubscriptionChangeDelivered_RT15_28(t *testing.T) {
	f := newTimerFixture(t)
	startStd(t, f, "r")
	s := f.openSSE(t, "r")
	s.next(t) // current (running)
	f.modControl(t, "r", "pause", nil)
	// Read events until we observe the paused state.
	for i := 0; i < 5; i++ {
		if s.next(t).Paused {
			return
		}
	}
	t.Error("pause was not delivered to the open subscription")
}

// --- AC15.8: single shared timer; convergence ---

func TestTimer_ConcurrentActionsConverge_RT15_29(t *testing.T) {
	f := newTimerFixture(t)
	f.modControl(t, "r", "set", stdConfig())
	// Two different valid moderator tokens act in sequence.
	if status, _ := f.control(t, "r", "start", nil, f.jwt(t, "*")); status != http.StatusOK {
		t.Fatalf("wildcard start status %d", status)
	}
	if status, _ := f.control(t, "r", "pause", nil, f.jwt(t, "r")); status != http.StatusOK {
		t.Fatalf("room-scoped pause status %d", status)
	}
	if sv := f.state(t, "r"); !sv.Paused {
		t.Errorf("after start then pause, paused=%v, want true (latest action wins)", sv.Paused)
	}
}

func TestTimer_SubscribersObserveSameState_RT15_30(t *testing.T) {
	f := newTimerFixture(t)
	startStd(t, f, "r")
	f.advance(120 * time.Second)
	a := f.state(t, "r")
	b := f.state(t, "r")
	if a != b {
		t.Errorf("two reads differ: %+v vs %+v", a, b)
	}
}

// --- AC15.9: per-room independence ---

func TestTimer_RoomsIndependent_RT15_31(t *testing.T) {
	f := newTimerFixture(t)
	startStd(t, f, "a")
	if sv := f.state(t, "b"); sv.Phase != "stopped" {
		t.Errorf("room b phase=%q after starting a, want stopped", sv.Phase)
	}
	if sv := f.state(t, "a"); !sv.Running {
		t.Errorf("room a not running")
	}
}

func TestTimer_IndependentPhasesConcurrent_RT15_32(t *testing.T) {
	f := newTimerFixture(t)
	f.modControl(t, "a", "set", map[string]any{"total": 300, "warnPercent": 20, "gracePercent": 30}) // warnAt 240
	f.modControl(t, "a", "start", nil)
	f.modControl(t, "b", "set", map[string]any{"total": 600, "warnPercent": 20, "gracePercent": 30}) // warnAt 480
	f.modControl(t, "b", "start", nil)
	f.advance(250 * time.Second)
	if sv := f.state(t, "a"); sv.Phase != "after-warning" {
		t.Errorf("room a phase=%q at 250s, want after-warning", sv.Phase)
	}
	if sv := f.state(t, "b"); sv.Phase != "before-warning" {
		t.Errorf("room b phase=%q at 250s, want before-warning", sv.Phase)
	}
}

// --- AC15.16: configuration persistence ---

func TestTimer_ConfigPersistsAcrossRestart_RT15_33(t *testing.T) {
	f := newTimerFixture(t)
	f.modControl(t, "p", "set", map[string]any{"total": 300, "warnPercent": 10, "gracePercent": 25})
	f.restart(t)
	sv := f.state(t, "p")
	if sv.Config.Total != 300 || sv.Config.WarnPercent != 10 || sv.Config.GracePercent != 25 {
		t.Errorf("after restart config=%+v, want 300/10/25", sv.Config)
	}
}

func TestTimer_ConfigLastWriteWinsAcrossRestart_RT15_34(t *testing.T) {
	f := newTimerFixture(t)
	f.modControl(t, "p", "set", map[string]any{"total": 300, "warnPercent": 10, "gracePercent": 25})
	f.modControl(t, "p", "set", map[string]any{"total": 600, "warnPercent": 15, "gracePercent": 20})
	f.restart(t)
	if got := f.state(t, "p").Config.Total; got != 600 {
		t.Errorf("after restart total=%d, want 600 (last write wins)", got)
	}
}

func TestTimer_DefaultsWhenUnset_RT15_35(t *testing.T) {
	f := newTimerFixture(t)
	f.restart(t)
	sv := f.state(t, "never")
	if sv.Config.Total != 900 || sv.Config.WarnPercent != 20 || sv.Config.GracePercent != 30 {
		t.Errorf("unset room config=%+v, want defaults 900/20/30", sv.Config)
	}
}

func TestTimer_RuntimeNotPersisted_RT15_36(t *testing.T) {
	f := newTimerFixture(t)
	f.modControl(t, "p", "set", map[string]any{"total": 300, "warnPercent": 20, "gracePercent": 30})
	f.modControl(t, "p", "start", nil)
	f.advance(50 * time.Second)
	f.restart(t)
	sv := f.state(t, "p")
	if sv.Phase != "stopped" {
		t.Errorf("after restart phase=%q, want stopped (runtime not persisted)", sv.Phase)
	}
	if sv.Config.Total != 300 {
		t.Errorf("after restart total=%d, want retained 300", sv.Config.Total)
	}
}
