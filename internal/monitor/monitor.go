// Package monitor runs an agent's watch scripts in the background, so the
// agent's run can end while it waits. A monitor is a shell script that runs
// in the channel's agent container until it exits, typically an `until`
// loop over some condition. When it exits or times out, the channel gets a
// message with the result, which starts the agent's next turn. A monitor
// that is cancelled wakes nobody.
//
// Monitors live in memory: a daemon restart stops them, also without a
// wake-up.
package monitor

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/radutopala/loop/internal/randutil"
)

// Event types published on the channel when a monitor starts and ends.
const (
	EventStarted  = "monitor.started"
	EventFinished = "monitor.finished"
)

const (
	// DefaultTimeout applies when Start gets no timeout.
	DefaultTimeout = time.Hour
	// MaxTimeout caps a monitor's timeout.
	MaxTimeout = 24 * time.Hour
	// MaxPerChannel caps the monitors running at once in one channel.
	MaxPerChannel = 10
	// outputTailBytes caps the script output quoted in the wake-up message.
	outputTailBytes = 4000
)

// ErrLimit is returned by Start when the channel already runs MaxPerChannel
// monitors.
var ErrLimit = fmt.Errorf("a channel can run at most %d monitors at once", MaxPerChannel)

// ErrStopped is returned by Start once the manager has stopped.
var ErrStopped = errors.New("monitors are shutting down")

// BashRunner runs a script in the channel's agent container and returns its
// output. container.DockerRunner implements it.
type BashRunner interface {
	RunBash(ctx context.Context, script, channelID, dirPath, parentDirPath string) (string, error)
}

// Monitor is one running watch script.
type Monitor struct {
	ID          string    `json:"id"`
	ChannelID   string    `json:"channel_id"`
	Description string    `json:"description"`
	Command     string    `json:"command"`
	StartedAt   time.Time `json:"started_at"`
	TimeoutSec  int       `json:"timeout_sec"`
	// Interrupt makes the wake-up cancel the agent's active run instead of
	// queueing behind it.
	Interrupt bool `json:"interrupt"`
}

// Finished is the payload of a monitor.finished event. Status says how the
// script ended: "exited with status 0", "timed out after 1h0m0s",
// "cancelled", or the error that stopped it.
type Finished struct {
	ID        string `json:"id"`
	ChannelID string `json:"channel_id"`
	Status    string `json:"status"`
}

type entry struct {
	Monitor
	ctx       context.Context
	cancel    context.CancelFunc
	cancelled bool
}

// Manager starts, lists and cancels monitors.
type Manager struct {
	runner BashRunner
	// wake posts content to the channel as a message the agent answers;
	// with interrupt, it cancels the active run instead of queueing.
	wake func(ctx context.Context, channelID, content string, interrupt bool)
	// publish broadcasts a channel event to the UI.
	publish func(channelID, eventType string, data any)
	logger  *slog.Logger
	newID   func() string
	now     func() time.Time

	mu       sync.Mutex
	monitors map[string]*entry
	stopped  bool
	wg       sync.WaitGroup
}

// NewManager returns a Manager that runs scripts with runner, posts wake-up
// messages with wake and publishes events with publish.
func NewManager(runner BashRunner, wake func(ctx context.Context, channelID, content string, interrupt bool), publish func(channelID, eventType string, data any), logger *slog.Logger) *Manager {
	return &Manager{
		runner:   runner,
		wake:     wake,
		publish:  publish,
		logger:   logger,
		newID:    func() string { return "mon-" + randutil.HexID(8) },
		now:      time.Now,
		monitors: map[string]*entry{},
	}
}

// Start runs command in channelID's agent container in the background and
// returns at once. A zero timeout means DefaultTimeout; it is capped at
// MaxTimeout. With interrupt, the wake-up cancels the agent's active run.
func (m *Manager) Start(channelID, dirPath, parentDirPath, command, description string, timeout time.Duration, interrupt bool) (*Monitor, error) {
	timeout = min(orDefault(timeout, DefaultTimeout), MaxTimeout)
	e, err := m.register(channelID, command, description, timeout, interrupt)
	if err != nil {
		return nil, err
	}
	// Published before the script starts, so monitor.started always comes
	// before monitor.finished.
	m.publish(channelID, EventStarted, e.Monitor)
	go m.run(e.ctx, e, dirPath, parentDirPath, timeout)
	mon := e.Monitor
	return &mon, nil
}

// register adds a monitor for channelID unless the manager has stopped or
// the channel is at its limit.
func (m *Manager) register(channelID, command, description string, timeout time.Duration, interrupt bool) (*entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopped {
		return nil, ErrStopped
	}
	n := 0
	for _, e := range m.monitors {
		if e.ChannelID == channelID {
			n++
		}
	}
	if n >= MaxPerChannel {
		return nil, ErrLimit
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	e := &entry{
		Monitor: Monitor{
			ID:          m.newID(),
			ChannelID:   channelID,
			Description: description,
			Command:     command,
			StartedAt:   m.now(),
			TimeoutSec:  int(timeout / time.Second),
			Interrupt:   interrupt,
		},
		ctx:    ctx,
		cancel: cancel,
	}
	m.monitors[e.ID] = e
	m.wg.Add(1)
	return e, nil
}

func (m *Manager) run(ctx context.Context, e *entry, dirPath, parentDirPath string, timeout time.Duration) {
	defer m.wg.Done()
	output, err := m.runner.RunBash(ctx, e.Command, e.ChannelID, dirPath, parentDirPath)
	timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)
	e.cancel()

	m.mu.Lock()
	delete(m.monitors, e.ID)
	cancelled := e.cancelled || (m.stopped && !timedOut)
	m.mu.Unlock()

	status := "exited with status 0"
	switch {
	case cancelled:
		status = "cancelled"
	case timedOut:
		status = "timed out after " + timeout.String()
	case err != nil:
		status = err.Error()
		if after, ok := strings.CutPrefix(status, "script "); ok {
			status = after
		}
	}
	m.logger.Info("monitor finished", "id", e.ID, "channel_id", e.ChannelID, "status", status)
	m.publish(e.ChannelID, EventFinished, Finished{ID: e.ID, ChannelID: e.ChannelID, Status: status})
	if cancelled {
		return
	}
	m.wake(context.Background(), e.ChannelID, wakeMessage(e.Monitor, status, output), e.Interrupt)
}

// wakeMessage is what the channel gets when a monitor ends.
func wakeMessage(mon Monitor, status, output string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[monitor %s] %q %s.", mon.ID, mon.Description, status)
	output = strings.TrimSpace(stripPreamble(output))
	if output == "" {
		b.WriteString(" No output.")
		return b.String()
	}
	if len(output) > outputTailBytes {
		output = "…" + output[len(output)-outputTailBytes:]
	}
	fmt.Fprintf(&b, " Output:\n```\n%s\n```", output)
	return b.String()
}

// stripPreamble drops the docker proxy's startup log line, which the agent
// container prints before the script runs.
func stripPreamble(output string) string {
	first, rest, _ := strings.Cut(output, "\n")
	if strings.Contains(first, `msg="loop-dockerproxy started"`) {
		return rest
	}
	return output
}

// List returns channelID's running monitors, oldest first.
func (m *Manager) List(channelID string) []Monitor {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []Monitor{}
	for _, e := range m.monitors {
		if e.ChannelID == channelID {
			out = append(out, e.Monitor)
		}
	}
	slices.SortFunc(out, func(a, b Monitor) int {
		if c := a.StartedAt.Compare(b.StartedAt); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}

// Cancel stops channelID's monitor id without waking the agent. It reports
// whether that monitor was running.
func (m *Manager) Cancel(channelID, id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.monitors[id]
	if !ok || e.ChannelID != channelID {
		return false
	}
	e.cancelled = true
	e.cancel()
	return true
}

// Stop cancels every monitor and waits for their containers to stop. Start
// fails from then on.
func (m *Manager) Stop() {
	m.mu.Lock()
	m.stopped = true
	for _, e := range m.monitors {
		e.cancel()
	}
	m.mu.Unlock()
	m.wg.Wait()
}

func orDefault(d, def time.Duration) time.Duration {
	if d <= 0 {
		return def
	}
	return d
}
