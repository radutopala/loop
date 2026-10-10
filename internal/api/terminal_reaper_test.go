package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/container"
)

// listingTerminalManager is a MockTerminalManager that lists its sessions.
type listingTerminalManager struct {
	*MockTerminalManager
	sessions map[string]bool
}

func (m *listingTerminalManager) Sessions() map[string]bool { return m.sessions }

type TerminalReaperSuite struct {
	suite.Suite
	srv    *Server
	docker *listingTerminalManager
	host   *listingTerminalManager
	now    time.Time
}

func TestTerminalReaperSuite(t *testing.T) {
	suite.Run(t, new(TerminalReaperSuite))
}

func (s *TerminalReaperSuite) SetupTest() {
	s.srv = nilServer()
	s.docker = &listingTerminalManager{MockTerminalManager: new(MockTerminalManager), sessions: map[string]bool{}}
	s.host = &listingTerminalManager{MockTerminalManager: new(MockTerminalManager), sessions: map[string]bool{}}
	s.srv.SetTerminalManager(s.docker)
	s.srv.SetHostTerminalManager(s.host)
	s.now = time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	s.srv.termClaims.now = func() time.Time { return s.now }
}

// sweep moves the clock on by d and runs one reaper pass.
func (s *TerminalReaperSuite) sweep(d time.Duration) {
	s.now = s.now.Add(d)
	s.srv.reapUnclaimedTerminals(context.Background(), 5*time.Minute, time.Minute)
}

func (s *TerminalReaperSuite) TestReapsSessionsNoWindowHolds() {
	tests := []struct {
		name     string
		host     bool
		attached bool
		claimed  bool
		reaped   bool
	}{
		{name: "docker shell no window holds", reaped: true},
		{name: "host shell no window holds", host: true, reaped: true},
		{name: "shell a window keeps claiming", claimed: true},
		{name: "shell a pane is attached to", attached: true},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			mgr := s.docker
			if tt.host {
				mgr = s.host
			}
			mgr.sessions["sess-1"] = tt.attached
			mgr.On("KillProcessGroup", mock.Anything, "sess-1").Return(nil).Maybe()
			mgr.On("StopSession", "sess-1").Return("ctr-1", nil).Maybe()

			// A session seen for the first time gets the whole grace.
			s.sweep(0)
			for range 6 {
				if tt.claimed {
					s.srv.termClaims.claim([]string{"sess-1"})
				}
				s.sweep(time.Minute)
			}

			if !tt.reaped {
				mgr.AssertNotCalled(s.T(), "StopSession", mock.Anything)
				return
			}
			mgr.AssertCalled(s.T(), "StopSession", "sess-1")
			if tt.host {
				mgr.AssertNotCalled(s.T(), "KillProcessGroup", mock.Anything, mock.Anything)
			} else {
				mgr.AssertCalled(s.T(), "KillProcessGroup", mock.Anything, "sess-1")
			}
		})
	}
}

func (s *TerminalReaperSuite) TestGraceRunsFromTheLastClaim() {
	s.docker.sessions["sess-1"] = false
	s.docker.On("KillProcessGroup", mock.Anything, "sess-1").Return(nil)
	s.docker.On("StopSession", "sess-1").Return("ctr-1", nil)

	s.srv.termClaims.claim([]string{"sess-1"})
	for range 5 {
		s.sweep(time.Minute)
	}
	s.docker.AssertNotCalled(s.T(), "StopSession", mock.Anything)
	s.sweep(time.Minute)
	s.docker.AssertCalled(s.T(), "StopSession", "sess-1")
}

func (s *TerminalReaperSuite) TestSleepCountsAsHeld() {
	s.docker.sessions["sess-1"] = false
	s.sweep(0)
	// The machine slept for an hour: no window could claim meanwhile.
	s.sweep(time.Hour)
	s.sweep(time.Minute)
	s.docker.AssertNotCalled(s.T(), "StopSession", mock.Anything)
}

func (s *TerminalReaperSuite) TestReleasesTheShellContainer() {
	reg := new(mockContainerManager)
	reg.containers = []*container.ContainerInfo{{ContainerID: "ctr-1", ChannelID: "ch-1", Type: container.ContainerTypeShell, Status: container.ContainerStatusRunning}}
	reg.On("ScheduleRemove", "ctr-1", 3*time.Minute).Return()
	s.srv.containerRegistry = reg
	s.srv.containerKeepAlive = 3 * time.Minute
	s.docker.sessions["sess-1"] = false
	s.docker.On("KillProcessGroup", mock.Anything, "sess-1").Return(errors.New("kill failed"))
	s.docker.On("StopSession", "sess-1").Return("ctr-1", nil)
	s.docker.On("LiveSessions", "ctr-1").Return(0)

	s.sweep(0)
	for range 6 {
		s.sweep(time.Minute)
	}

	// The kill failing still stops the session.
	s.docker.AssertCalled(s.T(), "StopSession", "sess-1")
	reg.AssertCalled(s.T(), "ScheduleRemove", "ctr-1", 3*time.Minute)
}

func (s *TerminalReaperSuite) TestStopFailureSkipsRelease() {
	s.docker.sessions["sess-1"] = false
	s.docker.On("KillProcessGroup", mock.Anything, "sess-1").Return(nil)
	s.docker.On("StopSession", "sess-1").Return("", errors.New("session not found"))

	s.sweep(0)
	for range 6 {
		s.sweep(time.Minute)
	}
	s.docker.AssertCalled(s.T(), "StopSession", "sess-1")
	s.docker.AssertNotCalled(s.T(), "LiveSessions", mock.Anything)
}

func (s *TerminalReaperSuite) TestForgetsSessionsThatAreGone() {
	s.srv.termClaims.claim([]string{"sess-gone"})
	s.docker.sessions["sess-1"] = true
	s.sweep(time.Minute)
	require.Equal(s.T(), map[string]time.Time{"sess-1": s.now}, s.srv.termClaims.lastHeld)
}

func (s *TerminalReaperSuite) TestManagersThatCantList() {
	s.srv.termManager = new(MockTerminalManager)
	s.srv.hostTermManager = nil
	s.sweep(time.Minute)
	require.Empty(s.T(), s.srv.termClaims.lastHeld)
}

func (s *TerminalReaperSuite) TestClaimHandler() {
	tests := []struct {
		name     string
		body     string
		wantCode int
		claimed  []string
	}{
		{name: "claims the sessions", body: `{"session_ids":["sess-1","sess-2"]}`, wantCode: http.StatusNoContent, claimed: []string{"sess-1", "sess-2"}},
		{name: "invalid body", body: `{`, wantCode: http.StatusBadRequest},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			srv := nilServer()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/terminal/claims", strings.NewReader(tt.body))
			srv.buildMux().ServeHTTP(rec, req)
			require.Equal(s.T(), tt.wantCode, rec.Code)
			got := make([]string, 0, len(srv.termClaims.lastHeld))
			for id, at := range srv.termClaims.lastHeld {
				got = append(got, id)
				require.WithinDuration(s.T(), time.Now(), at, time.Minute)
			}
			require.ElementsMatch(s.T(), tt.claimed, got)
		})
	}
}

func (s *TerminalReaperSuite) TestRunTerminalReaper() {
	s.docker.sessions["sess-1"] = true
	s.srv.termClaims.now = nil
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.srv.runTerminalReaper(ctx, time.Minute, time.Millisecond)
		close(done)
	}()
	require.Eventually(s.T(), func() bool {
		s.srv.termClaims.mu.Lock()
		defer s.srv.termClaims.mu.Unlock()
		_, ok := s.srv.termClaims.lastHeld["sess-1"]
		return ok
	}, 2*time.Second, time.Millisecond)
	cancel()
	<-done

	// The exported runner stops with its context.
	s.srv.RunTerminalReaper(ctx)
}
