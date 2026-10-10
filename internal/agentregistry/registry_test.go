package agentregistry

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type RegistrySuite struct {
	suite.Suite
	reg *Registry
}

func TestRegistrySuite(t *testing.T) {
	suite.Run(t, new(RegistrySuite))
}

func (s *RegistrySuite) SetupTest() {
	s.reg = New()
	s.reg.timeNow = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
}

func (s *RegistrySuite) TestRegisterAndList() {
	s.reg.Register(&AgentInfo{AgentID: "agent-0", ChannelID: "ch-1", Name: "Alpha"})
	s.reg.Register(&AgentInfo{AgentID: "agent-1", ChannelID: "ch-1", Name: "Beta"})
	s.reg.Register(&AgentInfo{AgentID: "agent-0", ChannelID: "ch-2", Name: "Gamma"})

	agents := s.reg.List("ch-1")
	require.Len(s.T(), agents, 2)

	agents2 := s.reg.List("ch-2")
	require.Len(s.T(), agents2, 1)
	require.Equal(s.T(), "Gamma", agents2[0].Name)
}

func (s *RegistrySuite) TestRegisterSetsDefaults() {
	s.reg.Register(&AgentInfo{AgentID: "a-0", ChannelID: "ch-1"})
	agent := s.reg.Get("ch-1", "a-0")
	require.NotNil(s.T(), agent)
	require.Equal(s.T(), "idle", agent.Status)
	require.Equal(s.T(), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), agent.CreatedAt)
	require.Equal(s.T(), agent.CreatedAt, agent.UpdatedAt)
}

func (s *RegistrySuite) TestRegisterDuplicateUpdates() {
	s.reg.Register(&AgentInfo{AgentID: "a-0", ChannelID: "ch-1", Name: "v1", Status: "idle"})
	created := s.reg.Get("ch-1", "a-0").CreatedAt

	s.reg.timeNow = func() time.Time { return time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC) }
	s.reg.Register(&AgentInfo{AgentID: "a-0", ChannelID: "ch-1", Name: "v2", Status: "running"})

	agent := s.reg.Get("ch-1", "a-0")
	require.Equal(s.T(), "v2", agent.Name)
	require.Equal(s.T(), "running", agent.Status)
	require.Equal(s.T(), created, agent.CreatedAt, "CreatedAt should be preserved")
	require.True(s.T(), agent.UpdatedAt.After(created))
}

func (s *RegistrySuite) TestUnregister() {
	s.reg.Register(&AgentInfo{AgentID: "a-0", ChannelID: "ch-1"})
	s.reg.Unregister("ch-1", "a-0")

	require.Nil(s.T(), s.reg.Get("ch-1", "a-0"))
	require.Empty(s.T(), s.reg.List("ch-1"))
}

func (s *RegistrySuite) TestUnregisterIdempotent() {
	s.reg.Register(&AgentInfo{AgentID: "a-0", ChannelID: "ch-1"})
	s.reg.Unregister("ch-1", "a-0")
	s.reg.Unregister("ch-1", "a-0") // no panic
	s.reg.Unregister("ch-1", "nonexistent")
	s.reg.Unregister("nonexistent", "a-0")
}

func (s *RegistrySuite) TestUnregisterCleansUpChannel() {
	s.reg.Register(&AgentInfo{AgentID: "a-0", ChannelID: "ch-1"})
	s.reg.Unregister("ch-1", "a-0")

	s.reg.mu.RLock()
	_, hasAgents := s.reg.agents["ch-1"]
	s.reg.mu.RUnlock()

	require.False(s.T(), hasAgents, "channel should be removed from agents map")
}

func (s *RegistrySuite) TestGetNonExistent() {
	require.Nil(s.T(), s.reg.Get("ch-1", "nonexistent"))
	require.Nil(s.T(), s.reg.Get("nonexistent", "a-0"))
}

func (s *RegistrySuite) TestUpdateStatus() {
	s.reg.Register(&AgentInfo{AgentID: "a-0", ChannelID: "ch-1", Status: "idle"})

	s.reg.timeNow = func() time.Time { return time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC) }
	updated := s.reg.UpdateStatus("ch-1", "a-0", "running", "indexing files", "Worker")

	require.NotNil(s.T(), updated)
	require.Equal(s.T(), "running", updated.Status)
	require.Equal(s.T(), "indexing files", updated.WorkSummary)
	require.Equal(s.T(), "Worker", updated.Name)
	require.Equal(s.T(), time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC), updated.UpdatedAt)
}

func (s *RegistrySuite) TestUpdateStatusPartial() {
	s.reg.Register(&AgentInfo{AgentID: "a-0", ChannelID: "ch-1", Name: "Alpha", Status: "idle"})

	// Only update status, leave name and summary unchanged.
	updated := s.reg.UpdateStatus("ch-1", "a-0", "running", "", "")
	require.Equal(s.T(), "running", updated.Status)
	require.Equal(s.T(), "Alpha", updated.Name)
}

func (s *RegistrySuite) TestUpdateStatusOnlyWorkSummary() {
	s.reg.Register(&AgentInfo{AgentID: "a-0", ChannelID: "ch-1", Name: "Alpha", Status: "idle"})
	updated := s.reg.UpdateStatus("ch-1", "a-0", "", "indexing files", "")
	require.Equal(s.T(), "idle", updated.Status)
	require.Equal(s.T(), "indexing files", updated.WorkSummary)
	require.Equal(s.T(), "Alpha", updated.Name)
}

func (s *RegistrySuite) TestUpdateStatusOnlyName() {
	s.reg.Register(&AgentInfo{AgentID: "a-0", ChannelID: "ch-1", Name: "Alpha", Status: "idle", WorkSummary: "old"})
	updated := s.reg.UpdateStatus("ch-1", "a-0", "", "", "Beta")
	require.Equal(s.T(), "idle", updated.Status)
	require.Equal(s.T(), "old", updated.WorkSummary)
	require.Equal(s.T(), "Beta", updated.Name)
}

func (s *RegistrySuite) TestUpdateStatusNonExistent() {
	// Channel does not exist at all.
	require.Nil(s.T(), s.reg.UpdateStatus("nope", "a-0", "running", "", ""))

	// Channel exists but agent ID does not.
	s.reg.Register(&AgentInfo{AgentID: "a-0", ChannelID: "ch-1"})
	require.Nil(s.T(), s.reg.UpdateStatus("ch-1", "unknown-agent", "running", "", ""))
}

func (s *RegistrySuite) TestListEmptyChannel() {
	result := s.reg.List("nonexistent")
	require.NotNil(s.T(), result)
	require.Empty(s.T(), result)
}

func (s *RegistrySuite) TestConcurrentAccess() {
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("agent-%d", i)
			s.reg.Register(&AgentInfo{AgentID: id, ChannelID: "ch-1"})
			s.reg.Get("ch-1", id)
			s.reg.List("ch-1")
			s.reg.UpdateStatus("ch-1", id, "running", "", "")
			s.reg.Unregister("ch-1", id)
		}(i)
	}
	wg.Wait()
}

func (s *RegistrySuite) TestTerminals() {
	require.Empty(s.T(), s.reg.Terminal("ch-1", "a-0"))

	s.reg.SetTerminal("ch-1", "a-0", "sess-1")
	s.reg.SetTerminal("ch-1", "a-1", "sess-2")
	require.Equal(s.T(), "sess-1", s.reg.Terminal("ch-1", "a-0"))

	// The terminal outlives the agent's registration.
	s.reg.Register(&AgentInfo{AgentID: "a-0", ChannelID: "ch-1"})
	s.reg.Unregister("ch-1", "a-0")
	require.Equal(s.T(), "sess-1", s.reg.Terminal("ch-1", "a-0"))

	tests := []struct {
		name      string
		agentID   string
		sessionID string
		want0     string
		want1     string
	}{
		{name: "another session leaves it", agentID: "a-0", sessionID: "sess-old", want0: "sess-1", want1: "sess-2"},
		{name: "unknown agent", agentID: "a-9", sessionID: "sess-1", want0: "sess-1", want1: "sess-2"},
		{name: "its session clears it", agentID: "a-0", sessionID: "sess-1", want0: "", want1: "sess-2"},
		{name: "the last one empties the channel", agentID: "a-1", sessionID: "sess-2", want0: "", want1: ""},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.reg.ClearTerminal("ch-1", tt.agentID, tt.sessionID)
			require.Equal(s.T(), tt.want0, s.reg.Terminal("ch-1", "a-0"))
			require.Equal(s.T(), tt.want1, s.reg.Terminal("ch-1", "a-1"))
		})
	}
	require.NotContains(s.T(), s.reg.terminals, "ch-1")
	s.reg.ClearTerminal("ch-2", "a-0", "sess-1")
}
