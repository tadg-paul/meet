// ABOUTME: Shared per-room meeting timer (issue #15). Server-authoritative
// ABOUTME: state machine; phase is derived from elapsed run time and the clock.

package server

import (
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// Timer phases. The phase is a pure function of the configuration and the
// elapsed run time.
const (
	PhaseStopped       = "stopped"
	PhaseBeforeWarning = "before-warning"
	PhaseAfterWarning  = "after-warning"
	PhaseGrace         = "grace"
	PhaseExceeded      = "exceeded"
)

// flashWindow is how long the grace-exceeded phase lasts before the timer
// auto-resets to stopped (the black/red flash on the banner).
const flashWindow = 10 * time.Second

// StateView is the timer state exposed to clients over HTTP/SSE.
type StateView struct {
	Phase     string      `json:"phase"`
	Elapsed   int         `json:"elapsed"`
	Remaining int         `json:"remaining"`
	CountUp   int         `json:"countUp"`
	Running   bool        `json:"running"`
	Paused    bool        `json:"paused"`
	Extended  bool        `json:"extended"`
	Config    StateConfig `json:"config"`
}

// StateConfig is the configuration portion of StateView, including the derived
// second values so the client does not recompute them.
type StateConfig struct {
	Total        int `json:"total"`
	WarnPercent  int `json:"warnPercent"`
	GracePercent int `json:"gracePercent"`
	WarnSeconds  int `json:"warnSeconds"`
	GraceSeconds int `json:"graceSeconds"`
}

// roomTimer is the in-memory runtime state of one room's timer. It is not
// persisted; a server restart clears it (issue #15, AC15.16).
type roomTimer struct {
	cfg          TimerConfig
	elapsedBase  time.Duration // run time accumulated before the current run segment
	runningSince time.Time     // start of the current run segment (valid when running)
	running      bool
	extended     bool
}

// TimerHub holds every room's timer and fans state changes out to subscribers.
type TimerHub struct {
	mu       sync.Mutex
	now      func() time.Time
	settings *TimerSettingsLog
	rooms    map[string]*roomTimer
	subs     map[string]map[chan StateView]struct{}
	logger   *slog.Logger
}

// NewTimerHub builds a hub. settings may be nil (configuration then falls back
// to the defaults and set actions are rejected).
func NewTimerHub(settings *TimerSettingsLog, now func() time.Time, logger *slog.Logger) *TimerHub {
	if now == nil {
		now = time.Now
	}
	return &TimerHub{
		now:      now,
		settings: settings,
		rooms:    make(map[string]*roomTimer),
		subs:     make(map[string]map[chan StateView]struct{}),
		logger:   logger,
	}
}

func (h *TimerHub) configFor(room string) TimerConfig {
	if h.settings == nil {
		return DefaultTimerConfig
	}
	return h.settings.ConfigFor(room)
}

// Apply mutates the room's timer for the given action and returns the resulting
// state. A control error (unknown action, invalid or missing config) leaves the
// state unchanged and is returned to the caller.
func (h *TimerHub) Apply(room, action string, cfg *TimerConfig) (StateView, error) {
	h.mu.Lock()

	var err error
	switch action {
	case "set":
		if cfg == nil {
			err = fmt.Errorf("set requires a configuration")
			break
		}
		if verr := cfg.Valid(); verr != nil {
			err = verr
			break
		}
		if h.settings == nil {
			err = fmt.Errorf("timer settings not configured")
			break
		}
		err = h.settings.Append(room, *cfg, h.now())
	case "start", "restart":
		h.rooms[room] = &roomTimer{
			cfg:          h.configFor(room),
			runningSince: h.now(),
			running:      true,
		}
	case "pause":
		if rt := h.rooms[room]; rt != nil && rt.running {
			rt.elapsedBase += h.now().Sub(rt.runningSince)
			rt.running = false
		}
	case "resume":
		if rt := h.rooms[room]; rt != nil && !rt.running {
			rt.runningSince = h.now()
			rt.running = true
		}
	case "extend":
		if rt := h.rooms[room]; rt != nil {
			rt.extended = true
		}
	case "reset", "stop":
		delete(h.rooms, room)
	default:
		err = fmt.Errorf("unknown action %q", action)
	}

	if err != nil {
		h.mu.Unlock()
		return StateView{}, err
	}

	view := h.stateForLocked(room)
	targets := h.subscribersLocked(room)
	h.mu.Unlock()

	broadcast(targets, view)
	return view, nil
}

// StateFor returns the room's current timer state.
func (h *TimerHub) StateFor(room string) StateView {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.stateForLocked(room)
}

// stateForLocked computes the current state. Caller holds mu. It lazily clears a
// timer that has run past its grace/flash window (auto-reset).
func (h *TimerHub) stateForLocked(room string) StateView {
	rt := h.rooms[room]
	if rt == nil {
		return stoppedView(h.configFor(room))
	}

	elapsed := rt.elapsedBase
	if rt.running {
		elapsed += h.now().Sub(rt.runningSince)
	}
	e := int(elapsed / time.Second)

	total := rt.cfg.Total
	warnAt := total - rt.cfg.WarnSeconds()
	graceLimit := rt.cfg.GraceSeconds()

	view := StateView{
		Extended: rt.extended,
		Config:   configView(rt.cfg),
	}

	switch {
	case e < warnAt:
		view.Phase = PhaseBeforeWarning
		view.Remaining = total - e
	case e < total:
		view.Phase = PhaseAfterWarning
		view.Remaining = total - e
	default:
		view.CountUp = e - total
		switch {
		case rt.extended, e < total+graceLimit:
			view.Phase = PhaseGrace
		case e < total+graceLimit+int(flashWindow/time.Second):
			view.Phase = PhaseExceeded
		default:
			delete(h.rooms, room)
			return stoppedView(rt.cfg)
		}
	}

	view.Elapsed = e
	view.Running = rt.running
	view.Paused = !rt.running
	return view
}

func stoppedView(cfg TimerConfig) StateView {
	return StateView{Phase: PhaseStopped, Config: configView(cfg)}
}

func configView(c TimerConfig) StateConfig {
	return StateConfig{
		Total:        c.Total,
		WarnPercent:  c.WarnPercent,
		GracePercent: c.GracePercent,
		WarnSeconds:  c.WarnSeconds(),
		GraceSeconds: c.GraceSeconds(),
	}
}

// Subscribe registers a subscriber for a room and immediately delivers the
// current state as the first event. The returned channel is closed by
// Unsubscribe.
func (h *TimerHub) Subscribe(room string) chan StateView {
	ch := make(chan StateView, 32)
	h.mu.Lock()
	if h.subs[room] == nil {
		h.subs[room] = make(map[chan StateView]struct{})
	}
	h.subs[room][ch] = struct{}{}
	current := h.stateForLocked(room)
	h.mu.Unlock()

	ch <- current // buffered; safe
	return ch
}

// Unsubscribe removes a subscriber and closes its channel.
func (h *TimerHub) Unsubscribe(room string, ch chan StateView) {
	h.mu.Lock()
	if subs := h.subs[room]; subs != nil {
		if _, ok := subs[ch]; ok {
			delete(subs, ch)
			close(ch)
		}
		if len(subs) == 0 {
			delete(h.subs, room)
		}
	}
	h.mu.Unlock()
}

// subscribersLocked returns the current subscriber channels for a room. Caller
// holds mu.
func (h *TimerHub) subscribersLocked(room string) []chan StateView {
	subs := h.subs[room]
	if len(subs) == 0 {
		return nil
	}
	out := make([]chan StateView, 0, len(subs))
	for ch := range subs {
		out = append(out, ch)
	}
	return out
}

// broadcast delivers a state view to each channel without blocking; a full
// channel (a slow client) drops the update and corrects on the next event.
func broadcast(targets []chan StateView, view StateView) {
	for _, ch := range targets {
		select {
		case ch <- view:
		default:
		}
	}
}
