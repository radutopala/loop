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
	_, hasMailboxes := s.reg.mailboxes["ch-1"]
	s.reg.mu.RUnlock()

	require.False(s.T(), hasAgents, "channel should be removed from agents map")
	require.False(s.T(), hasMailboxes, "channel should be removed from mailboxes map")
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

func (s *RegistrySuite) TestSendMessage() {
	s.reg.Register(&AgentInfo{AgentID: "a-0", ChannelID: "ch-1"})
	s.reg.Register(&AgentInfo{AgentID: "a-1", ChannelID: "ch-1"})

	err := s.reg.SendMessage("ch-1", "a-0", "a-1", "hello")
	require.NoError(s.T(), err)

	ch, err := s.reg.Subscribe("ch-1", "a-1")
	require.NoError(s.T(), err)

	select {
	case msg := <-ch:
		require.Equal(s.T(), "a-0", msg.FromAgentID)
		require.Equal(s.T(), "hello", msg.Content)
	case <-time.After(time.Second):
		s.T().Fatal("timeout waiting for message")
	}
}

func (s *RegistrySuite) TestSendMessageToNonExistent() {
	s.reg.Register(&AgentInfo{AgentID: "a-0", ChannelID: "ch-1"})

	err := s.reg.SendMessage("ch-1", "a-0", "nonexistent", "hello")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "not found")

	err = s.reg.SendMessage("nonexistent", "a-0", "a-1", "hello")
	require.Error(s.T(), err)
}

func (s *RegistrySuite) TestSendMessageDropsWhenFull() {
	s.reg.Register(&AgentInfo{AgentID: "a-0", ChannelID: "ch-1"})

	// Fill the mailbox.
	for i := range mailboxSize {
		err := s.reg.SendMessage("ch-1", "sender", "a-0", fmt.Sprintf("msg-%d", i))
		require.NoError(s.T(), err)
	}

	// Next message should be dropped.
	err := s.reg.SendMessage("ch-1", "sender", "a-0", "overflow")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "mailbox full")
}

func (s *RegistrySuite) TestSubscribe() {
	s.reg.Register(&AgentInfo{AgentID: "a-0", ChannelID: "ch-1"})

	ch, err := s.reg.Subscribe("ch-1", "a-0")
	require.NoError(s.T(), err)
	require.NotNil(s.T(), ch)
}

func (s *RegistrySuite) TestSubscribeNonExistentChannel() {
	_, err := s.reg.Subscribe("nope", "a-0")
	require.Error(s.T(), err)
}

func (s *RegistrySuite) TestSubscribeNonExistentAgent() {
	// Channel exists but agent doesn't.
	s.reg.Register(&AgentInfo{AgentID: "a-0", ChannelID: "ch-1"})
	_, err := s.reg.Subscribe("ch-1", "nonexistent")
	require.Error(s.T(), err)
}

func (s *RegistrySuite) TestSubscribeClosedOnUnregister() {
	s.reg.Register(&AgentInfo{AgentID: "a-0", ChannelID: "ch-1"})

	ch, err := s.reg.Subscribe("ch-1", "a-0")
	require.NoError(s.T(), err)

	s.reg.Unregister("ch-1", "a-0")

	// Channel should be closed.
	_, ok := <-ch
	require.False(s.T(), ok)
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
			_ = s.reg.SendMessage("ch-1", id, id, "self")
			s.reg.Unregister("ch-1", id)
		}(i)
	}
	wg.Wait()
}
