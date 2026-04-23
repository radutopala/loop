package orchestrator

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/agentgate"
	"github.com/radutopala/loop/internal/bot"
)

type GateAdapterSuite struct {
	suite.Suite
	mockBot *MockBot
	adapter *GateBotAdapter
	router  *GateBotRouter
}

func TestGateAdapterSuite(t *testing.T) {
	suite.Run(t, new(GateAdapterSuite))
}

func (s *GateAdapterSuite) SetupTest() {
	s.mockBot = new(MockBot)
	s.adapter = &GateBotAdapter{Bot: s.mockBot}
	s.router = &GateBotRouter{Bot: s.mockBot}
}

func (s *GateAdapterSuite) TestSendApprovalTranslatesRequestToPrompt() {
	req := agentgate.ApprovalRequest{
		ID:       "req-1",
		Kind:     "execve",
		Target:   "git push origin main",
		Message:  "git write-side operation",
		CacheKey: "execve:git:push origin", // must not leak to the bot
		Details:  map[string]string{"image": "alpine"},
	}
	expectedPrompt := bot.ApprovalPrompt{
		ID:      "req-1",
		Kind:    "execve",
		Target:  "git push origin main",
		Message: "git write-side operation",
		Details: map[string]string{"image": "alpine"},
	}
	s.mockBot.On("SendApproval", mock.Anything, "ch-1", expectedPrompt).Return("msg-1", nil)

	msgID, err := s.adapter.SendApproval(context.Background(), "ch-1", req)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "msg-1", msgID)
	s.mockBot.AssertExpectations(s.T())
}

func (s *GateAdapterSuite) TestSendApprovalPropagatesError() {
	s.mockBot.On("SendApproval", mock.Anything, "ch-1", mock.Anything).
		Return("", errors.New("boom"))

	_, err := s.adapter.SendApproval(context.Background(), "ch-1", agentgate.ApprovalRequest{ID: "x"})
	require.EqualError(s.T(), err, "boom")
}

func (s *GateAdapterSuite) TestRemoveApprovalForwards() {
	s.mockBot.On("RemoveApproval", mock.Anything, "ch-1", "msg-1").Return(nil)

	require.NoError(s.T(), s.adapter.RemoveApproval(context.Background(), "ch-1", "msg-1"))
	s.mockBot.AssertExpectations(s.T())
}

func (s *GateAdapterSuite) TestRouterForReturnsAdapterBackedByBot() {
	got := s.router.For("any-channel")
	require.NotNil(s.T(), got)

	// Any call on the returned agentgate.Bot should hit the underlying MockBot.
	s.mockBot.On("SendApproval", mock.Anything, "chan", mock.Anything).Return("m", nil)
	_, err := got.SendApproval(context.Background(), "chan", agentgate.ApprovalRequest{ID: "r"})
	require.NoError(s.T(), err)
	s.mockBot.AssertExpectations(s.T())
}

func (s *GateAdapterSuite) TestRouterForReturnsNilWhenBotIsNil() {
	r := &GateBotRouter{}
	require.Nil(s.T(), r.For("anything"))
}
