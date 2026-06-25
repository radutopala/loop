package orchestrator

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/testutil"
)

// ── parseSessionLimitReset ──

func TestParseSessionLimitReset(t *testing.T) {
	// A fixed "now": 2026-06-23 14:00 in Bucharest (UTC+3 in June).
	buch, err := time.LoadLocation("Europe/Bucharest")
	require.NoError(t, err)
	now := time.Date(2026, 6, 23, 14, 0, 0, 0, buch)

	tests := []struct {
		name    string
		msg     string
		wantOK  bool
		wantHHM string // expected "15:04" in the parsed location, when ok
		wantTZ  string
		// wantTomorrow asserts the reset rolled to the next day.
		wantTomorrow bool
	}{
		{
			name:    "pm with minutes, later today",
			msg:     "You've hit your session limit · resets 11:30pm (Europe/Bucharest)",
			wantOK:  true,
			wantHHM: "23:30",
			wantTZ:  "Europe/Bucharest",
		},
		{
			name:    "pm whole hour later today",
			msg:     "You've hit your session limit · resets 4pm (Europe/Bucharest)",
			wantOK:  true,
			wantHHM: "16:00",
			wantTZ:  "Europe/Bucharest",
		},
		{
			name:         "am earlier today rolls to tomorrow",
			msg:          "You've hit your limit · resets 8am (Europe/Bucharest)",
			wantOK:       true,
			wantHHM:      "08:00",
			wantTZ:       "Europe/Bucharest",
			wantTomorrow: true,
		},
		{
			name:         "1am rolls to tomorrow",
			msg:          "You've hit your session limit · resets 1am (Europe/Bucharest)",
			wantOK:       true,
			wantHHM:      "01:00",
			wantTZ:       "Europe/Bucharest",
			wantTomorrow: true,
		},
		{
			name:         "12am is midnight (rolls to tomorrow)",
			msg:          "You've hit your session limit · resets 12am (Europe/Bucharest)",
			wantOK:       true,
			wantHHM:      "00:00",
			wantTZ:       "Europe/Bucharest",
			wantTomorrow: true,
		},
		{
			name:    "12pm is noon (earlier today → tomorrow)",
			msg:     "You've hit your session limit · resets 12pm (Europe/Bucharest)",
			wantOK:  true,
			wantHHM: "12:00",
			wantTZ:  "Europe/Bucharest",
			// 12:00 < 14:00 now → tomorrow.
			wantTomorrow: true,
		},
		{
			name:    "UTC timezone",
			msg:     "You've hit your session limit · resets 11:30pm (UTC)",
			wantOK:  true,
			wantHHM: "23:30",
			wantTZ:  "UTC",
		},
		{name: "no reset clause (weekly usage limit)", msg: "Your usage limit reached.", wantOK: false},
		{name: "unparseable timezone", msg: "resets 5pm (Mars/Olympus)", wantOK: false},
		{name: "hour out of range", msg: "resets 13pm (UTC)", wantOK: false},
		{name: "minutes out of range", msg: "resets 5:75pm (UTC)", wantOK: false},
		{name: "plain transient error", msg: "Server is temporarily limiting requests", wantOK: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseSessionLimitReset(tc.msg, now)
			require.Equal(t, tc.wantOK, ok)
			if !tc.wantOK {
				return
			}
			loc, err := time.LoadLocation(tc.wantTZ)
			require.NoError(t, err)
			inLoc := got.In(loc)
			require.Equal(t, tc.wantHHM, inLoc.Format("15:04"))
			require.True(t, got.After(now), "reset must be strictly future")
			expectDay := now.In(loc).Day()
			if tc.wantTomorrow {
				expectDay = now.In(loc).Add(24 * time.Hour).Day()
			}
			require.Equal(t, expectDay, inLoc.Day())
		})
	}
}

func TestIsSessionLimitError(t *testing.T) {
	require.True(t, isSessionLimitError("You've hit your session limit · resets 8am (UTC)"))
	require.True(t, isSessionLimitError("You've hit your limit · resets 8am (UTC)"))
	require.False(t, isSessionLimitError("Your usage limit reached. Limit resets Monday."))
	require.False(t, isSessionLimitError("Server is temporarily limiting requests"))
}

func TestFormatWait(t *testing.T) {
	require.Equal(t, "less than a minute", formatWait(30*time.Second))
	require.Equal(t, "45m", formatWait(45*time.Minute))
	require.Equal(t, "9h27m", formatWait(9*time.Hour+27*time.Minute))
	require.Equal(t, "2h0m", formatWait(2*time.Hour))
}

// ── maybeScheduleSessionLimitRetry ──

type SessionLimitSuite struct {
	suite.Suite
	ctx       context.Context
	bot       *MockBot
	scheduler *testutil.MockScheduler
	orch      *Orchestrator
	now       time.Time
}

func (s *SessionLimitSuite) SetupTest() {
	s.ctx = context.Background()
	s.bot = new(MockBot)
	s.scheduler = new(testutil.MockScheduler)
	buch, _ := time.LoadLocation("Europe/Bucharest")
	s.now = time.Date(2026, 6, 23, 14, 0, 0, 0, buch)
	s.orch = &Orchestrator{
		bot:       s.bot,
		scheduler: s.scheduler,
		logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		timeNow:   func() time.Time { return s.now },
	}
	s.orch.cfg.Store(&config.Config{
		AgentRetry: config.AgentRetryConfig{SessionLimitAutoContinue: true},
	})
}

func TestSessionLimitSuite(t *testing.T) {
	suite.Run(t, new(SessionLimitSuite))
}

func (s *SessionLimitSuite) msg() *bot.IncomingMessage {
	return &bot.IncomingMessage{ChannelID: "ch-1", GuildID: "g-1", MessageID: "m-1"}
}

func (s *SessionLimitSuite) TestSchedulesOnceTaskAtResetTime() {
	s.scheduler.On("ListTasks", s.ctx, "ch-1").Return([]*db.ScheduledTask(nil), nil)
	var captured *db.ScheduledTask
	s.scheduler.On("AddTask", s.ctx, mock.MatchedBy(func(t *db.ScheduledTask) bool {
		captured = t
		return true
	})).Return(int64(7), nil)

	errMsg := "claude returned error: You've hit your session limit · resets 11:30pm (Europe/Bucharest)"
	notice, ok := s.orch.maybeScheduleSessionLimitRetry(s.ctx, s.msg(), errMsg)

	require.True(s.T(), ok)
	require.Contains(s.T(), notice, "automatically continue")
	require.NotNil(s.T(), captured)
	require.Equal(s.T(), db.TaskTypeOnce, captured.Type)
	require.Equal(s.T(), "ch-1", captured.ChannelID)
	require.Equal(s.T(), "continue", captured.Prompt)
	require.Equal(s.T(), sessionLimitTemplateName, captured.TemplateName)
	require.True(s.T(), captured.Enabled)
	// ThreadID == ChannelID so the retry runs IN the same thread/worktree-thread
	// (resuming its session inline), not in a freshly spawned child thread.
	require.Equal(s.T(), "ch-1", captured.ThreadID)
	// Schedule is RFC3339 at 23:30 Bucharest today.
	parsed, err := time.Parse(time.RFC3339, captured.Schedule)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "23:30", parsed.In(s.now.Location()).Format("15:04"))
	s.scheduler.AssertExpectations(s.T())
}

func (s *SessionLimitSuite) TestParseFailDoesNotSchedule() {
	// A session-limit string with an unparseable timezone → terminal, no AddTask.
	errMsg := "claude returned error: You've hit your session limit · resets 5pm (Mars/Olympus)"
	notice, ok := s.orch.maybeScheduleSessionLimitRetry(s.ctx, s.msg(), errMsg)
	require.False(s.T(), ok)
	require.Empty(s.T(), notice)
	s.scheduler.AssertNotCalled(s.T(), "AddTask", mock.Anything, mock.Anything)
}

func (s *SessionLimitSuite) TestNonSessionLimitErrorIgnored() {
	_, ok := s.orch.maybeScheduleSessionLimitRetry(s.ctx, s.msg(), "Your usage limit reached. Resets Monday.")
	require.False(s.T(), ok)
	s.scheduler.AssertNotCalled(s.T(), "AddTask", mock.Anything, mock.Anything)
}

func (s *SessionLimitSuite) TestDisabledByConfig() {
	s.orch.cfg.Store(&config.Config{
		AgentRetry: config.AgentRetryConfig{SessionLimitAutoContinue: false},
	})
	_, ok := s.orch.maybeScheduleSessionLimitRetry(s.ctx, s.msg(),
		"You've hit your session limit · resets 11:30pm (Europe/Bucharest)")
	require.False(s.T(), ok)
	s.scheduler.AssertNotCalled(s.T(), "AddTask", mock.Anything, mock.Anything)
}

func (s *SessionLimitSuite) TestDedupeSkipsWhenRetryAlreadyPending() {
	existing := []*db.ScheduledTask{{TemplateName: sessionLimitTemplateName, Enabled: true}}
	s.scheduler.On("ListTasks", s.ctx, "ch-1").Return(existing, nil)
	_, ok := s.orch.maybeScheduleSessionLimitRetry(s.ctx, s.msg(),
		"You've hit your session limit · resets 11:30pm (Europe/Bucharest)")
	require.False(s.T(), ok)
	s.scheduler.AssertNotCalled(s.T(), "AddTask", mock.Anything, mock.Anything)
}

func (s *SessionLimitSuite) TestAddTaskErrorReturnsFalse() {
	s.scheduler.On("ListTasks", s.ctx, "ch-1").Return([]*db.ScheduledTask(nil), nil)
	s.scheduler.On("AddTask", s.ctx, mock.Anything).Return(int64(0), errors.New("db down"))
	_, ok := s.orch.maybeScheduleSessionLimitRetry(s.ctx, s.msg(),
		"You've hit your session limit · resets 11:30pm (Europe/Bucharest)")
	require.False(s.T(), ok)
	s.scheduler.AssertExpectations(s.T())
}
