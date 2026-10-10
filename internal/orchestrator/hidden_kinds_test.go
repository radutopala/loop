package orchestrator

import (
	"context"
	"errors"

	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/db"
)

// TestPrepareAgentRequestPluginThread checks the hidden-kind machinery on a
// plugin's hidden thread: with nothing registered for plugins every run
// there is refused, and a handler registered under the prefix shapes the
// runs of every plugin's kind.
func (s *OrchestratorSuite) TestPrepareAgentRequestPluginThread() {
	thread := &db.Channel{ChannelID: "plugin-1", ParentID: "ch1", DirPath: "/project", Kind: db.PluginKind("cogtest")}
	parent := &db.Channel{ChannelID: "ch1", Name: "api", DirPath: "/project"}
	shape := hiddenKind{
		name: "plugin",
		apply: func(_ context.Context, req *agent.AgentRequest, msg *bot.IncomingMessage, p *db.Channel, turnID string) error {
			if msg.AuthorID == "bad" {
				return errors.New("bad run")
			}
			req.AgentID = "plugin-cogtest-generate:" + p.ChannelID + ":" + turnID
			req.ReadOnly = true
			return nil
		},
	}
	tests := []struct {
		name      string
		handler   *hiddenKind
		authorID  string
		parent    *db.Channel
		parentErr error
		wantErr   string
	}{
		{name: "nothing runs plugin threads", parent: parent, authorID: "loop-plugin", wantErr: `hidden thread plugin-1: nothing runs "plugin:cogtest" threads`},
		{name: "registered handler shapes the run", handler: &shape, parent: parent, authorID: "loop-plugin"},
		{name: "handler refuses", handler: &shape, parent: parent, authorID: "bad", wantErr: "bad run"},
		{name: "admit refuses", handler: &hiddenKind{name: "plugin", admit: func(id string, _ *bot.IncomingMessage) error { return errors.New("no runs in " + id) }}, parent: parent, wantErr: "no runs in plugin-1"},
		{name: "parent gone", handler: &shape, parentErr: errors.New("db down"), authorID: "loop-plugin", wantErr: `plugin thread plugin-1: parent "ch1" not found`},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			delete(s.orch.hiddenKinds, db.ChannelKindPluginPrefix)
			if tc.handler != nil {
				s.orch.hiddenKinds[db.ChannelKindPluginPrefix] = *tc.handler
			}
			s.store.On("GetRecentMessages", s.ctx, "plugin-1", recentMessageLimit).Return([]*db.Message{}, nil)
			s.store.On("GetChannel", s.ctx, "plugin-1").Return(thread, nil)
			s.store.On("GetChannel", s.ctx, "ch1").Return(tc.parent, tc.parentErr)

			req, _, _, err := s.orch.prepareAgentRequest(s.ctx, &bot.IncomingMessage{ChannelID: "plugin-1", AuthorID: tc.authorID, AuthorName: "loop", Content: "run"}, "b1")

			if tc.wantErr != "" {
				require.EqualError(s.T(), err, tc.wantErr)
				require.Nil(s.T(), req)
				return
			}
			require.NoError(s.T(), err)
			require.Equal(s.T(), "plugin-cogtest-generate:ch1:b1", req.AgentID)
			require.True(s.T(), req.ReadOnly)
		})
	}
}

// TestLookupHiddenKind checks which handler each kind of hidden thread gets.
func (s *OrchestratorSuite) TestLookupHiddenKind() {
	s.orch.hiddenKinds[db.ChannelKindPluginPrefix] = hiddenKind{name: "plugin"}
	for _, tc := range []struct {
		kind     string
		wantName string
	}{
		{db.ChannelKindLearn, "learn"},
		{db.ChannelKindExplain, "explain"},
		{db.PluginKind("cogtest"), "plugin"},
		{db.PluginKind("echo"), "plugin"},
		{"chat", ""},
	} {
		h, ok := s.orch.lookupHiddenKind(tc.kind)
		require.Equal(s.T(), tc.wantName != "", ok, "kind %q", tc.kind)
		require.Equal(s.T(), tc.wantName, h.name, "kind %q", tc.kind)
	}
}
