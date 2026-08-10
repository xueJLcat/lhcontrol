package autosleep

import (
	"fmt"
	"sync"
	"time"
)

// Target selects which process ending a session signals that the base
// stations can be put to sleep.
type Target string

const (
	// TargetSteam watches the Steam client process. It only fires once Steam
	// fully exits, so it never triggers for a Steam client left running in
	// the background.
	TargetSteam Target = "steam"
	// TargetSteamVR watches the SteamVR runtime. Stations exist to serve
	// VR, so the runtime going away is the most precise "session over"
	// signal even when the Steam client stays resident.
	TargetSteamVR Target = "steamvr"
)

// ProcessName returns the executable image name monitored for the target.
func (t Target) ProcessName() (string, error) {
	switch t {
	case TargetSteam:
		return "steam.exe", nil
	case TargetSteamVR:
		return "vrserver.exe", nil
	default:
		return "", fmt.Errorf("autosleep: unknown target %q", string(t))
	}
}

// Delay bounds keep the trigger sensible: a floor avoids firing on a brief
// process flicker (Steam self-update restart), the ceiling avoids a delay so
// long that the feature looks dead.
const (
	MinDelaySeconds = 60
	MaxDelaySeconds = 7200
	// DefaultDelaySeconds is five minutes, long enough to absorb Steam
	// self-update restarts and accidental closes.
	DefaultDelaySeconds = 300
)

// DefaultTarget is used when the persisted settings predate the field.
const DefaultTarget = TargetSteamVR

// Settings is the user-facing configuration for the automatic sleep feature.
type Settings struct {
	Enabled      bool   `json:"enabled"`
	Target       string `json:"target"`
	DelaySeconds int    `json:"delaySeconds"`
}

// DefaultSettings returns a disabled configuration with sane defaults.
func DefaultSettings() Settings {
	return Settings{
		Enabled:      false,
		Target:       string(DefaultTarget),
		DelaySeconds: DefaultDelaySeconds,
	}
}

// Validate returns a non-nil error when the settings cannot be applied.
func (s Settings) Validate() error {
	if _, err := Target(s.Target).ProcessName(); err != nil {
		return err
	}
	if s.DelaySeconds < MinDelaySeconds || s.DelaySeconds > MaxDelaySeconds {
		return fmt.Errorf(
			"autosleep: delay must be between %d and %d seconds, got %d",
			MinDelaySeconds, MaxDelaySeconds, s.DelaySeconds,
		)
	}
	return nil
}

// Delay returns the configured quiet period as a duration.
func (s Settings) Delay() time.Duration {
	return time.Duration(s.DelaySeconds) * time.Second
}

// Action reports what a Monitor poll decided.
type Action int

const (
	// ActionNone means nothing should happen for this poll.
	ActionNone Action = iota
	// ActionTrigger means the watched process exited long enough ago that the
	// configured action should run. The monitor has already consumed the
	// trigger; it will not fire again until the process runs and exits once
	// more.
	ActionTrigger
)

type monitorState int

const (
	stateIdle    monitorState = iota // process not running, waiting for a session
	stateRunning                     // process currently running
	stateClosed                      // session ended, delay counting down
)

// Monitor is a small deterministic state machine driven by periodic polls.
// It is intentionally free of time sources and process checks so it can be
// unit tested without the OS. The mutex only protects the concurrent
// Countdown snapshot taken when a watcher is replaced; a single watcher polls
// sequentially.
type Monitor struct {
	mutex    sync.Mutex
	delay    time.Duration
	state    monitorState
	closedAt time.Time
}

// NewMonitor returns a Monitor that fires once a session (running then
// closed) stays closed for at least delay.
func NewMonitor(delay time.Duration) *Monitor {
	if delay <= 0 {
		delay = time.Duration(DefaultDelaySeconds) * time.Second
	}
	return &Monitor{delay: delay}
}

// NewMonitorContinuing behaves like NewMonitor but starts already counting
// down a session close that happened at closedAt. A replacement watcher for
// the same watched target uses it so a settings change cannot silently drop
// a pending sleep.
func NewMonitorContinuing(delay time.Duration, closedAt time.Time) *Monitor {
	monitor := NewMonitor(delay)
	monitor.state = stateClosed
	monitor.closedAt = closedAt
	return monitor
}

// Countdown reports an in-flight "session closed" countdown, if any, so a
// replacement watcher can continue it instead of restarting from idle.
func (m *Monitor) Countdown() (active bool, closedAt time.Time) {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	return m.state == stateClosed, m.closedAt
}

// Poll advances the state machine with a fresh observation. running reports
// whether the watched process is alive; now is the poll timestamp.
func (m *Monitor) Poll(running bool, now time.Time) Action {
	m.mutex.Lock()
	defer m.mutex.Unlock()
	switch m.state {
	case stateIdle:
		if running {
			m.state = stateRunning
		}
		return ActionNone
	case stateRunning:
		if !running {
			m.state = stateClosed
			m.closedAt = now
		}
		return ActionNone
	case stateClosed:
		if running {
			// The user relaunched before the delay elapsed; abort the pending
			// trigger and re-arm for the next session.
			m.state = stateRunning
			return ActionNone
		}
		if now.Sub(m.closedAt) >= m.delay {
			m.state = stateIdle
			return ActionTrigger
		}
		return ActionNone
	}
	return ActionNone
}
