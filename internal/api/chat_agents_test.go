package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/agentregistry"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/orchestrator"
)

// chatStore gives the server a store with channel ch-1, its side chat
// side-1, and side chat side-9 of another channel.
func (s *ServerSuite) chatStore() *MockChannelLister {
	store := new(MockChannelLister)
	s.srv.store = store
	store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1"}, nil).Maybe()
	store.On("GetChannel", mock.Anything, "side-1").Return(&db.Channel{ChannelID: "side-1", Kind: db.ChannelKindSide, ParentID: "ch-1", Name: "Side"}, nil).Maybe()
	store.On("GetChannel", mock.Anything, "side-9").Return(&db.Channel{ChannelID: "side-9", Kind: db.ChannelKindSide, ParentID: "ch-9"}, nil).Maybe()
	store.On("GetChannel", mock.Anything, "side-x1").Return(&db.Channel{ChannelID: "side-x1"}, nil).Maybe()
	store.On("GetChannel", mock.Anything, mock.Anything).Return(nil, nil).Maybe()
	store.On("ListSideChats", mock.Anything, "ch-1").Return([]*db.Channel{{ChannelID: "side-1", Kind: db.ChannelKindSide, ParentID: "ch-1", Name: "Side"}}, nil).Maybe()
	return store
}

func (s *ServerSuite) listAgents(channelID string) (int, []agentListItem) {
	req := httptest.NewRequest("GET", "/api/agents?channel_id="+channelID, nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	var agents []agentListItem
	if w.Code == http.StatusOK {
		require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &agents))
	}
	return w.Code, agents
}

func (s *ServerSuite) TestAgentListChatAgents() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	reg.Register(&agentregistry.AgentInfo{AgentID: "a-0", ChannelID: "ch-1", Name: "Alpha"})

	type item struct {
		id, channel, name, status string
		self                      bool
	}
	tests := []struct {
		name    string
		channel string
		live    bool
		want    []item
	}{
		{
			name: "from the channel", channel: "ch-1", live: true,
			want: []item{{"chat", "ch-1", "Chat", "running", true}, {"side-1", "side-1", "Side", "idle", false}, {"a-0", "ch-1", "Alpha", "idle", false}},
		},
		{
			name: "from a side chat", channel: "side-1",
			want: []item{{"chat", "ch-1", "Chat", "idle", false}, {"side-1", "side-1", "Side", "idle", true}, {"a-0", "ch-1", "Alpha", "idle", false}},
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.chatStore()
			s.srv.live = nil
			if tt.live {
				live := new(mockLiveSessions)
				s.srv.live = live
				live.On("LiveSession", mock.Anything, "ch-1").Return(orchestrator.LiveStatus{Running: true, LiveInfo: agent.LiveInfo{Busy: true}})
				live.On("LiveSession", mock.Anything, "side-1").Return(orchestrator.LiveStatus{})
			}

			code, agents := s.listAgents(tt.channel)
			require.Equal(s.T(), http.StatusOK, code)
			got := make([]item, len(agents))
			for i, a := range agents {
				got[i] = item{a.AgentID, a.ChannelID, a.Name, a.Status, a.Self}
			}
			require.Equal(s.T(), tt.want, got)
		})
	}
}

func (s *ServerSuite) TestAgentListChatAgentsWithoutStore() {
	s.srv.SetAgentRegistry(agentregistry.New())
	s.srv.store = nil

	code, agents := s.listAgents("ch-1")
	require.Equal(s.T(), http.StatusOK, code)
	require.Empty(s.T(), agents)
}

func (s *ServerSuite) TestAgentListChatAgentsErrors() {
	tests := []struct {
		name  string
		setup func(store *MockChannelLister)
	}{
		{name: "channel lookup fails", setup: func(store *MockChannelLister) {
			store.On("GetChannel", mock.Anything, "ch-1").Return(nil, errors.New("db down"))
		}},
		{name: "group lookup fails", setup: func(store *MockChannelLister) {
			store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1"}, nil).Once()
			store.On("GetChannel", mock.Anything, "ch-1").Return(nil, errors.New("db down"))
		}},
		{name: "side chats fail", setup: func(store *MockChannelLister) {
			store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1"}, nil)
			store.On("ListSideChats", mock.Anything, "ch-1").Return(nil, errors.New("db down"))
		}},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.srv.SetAgentRegistry(agentregistry.New())
			store := new(MockChannelLister)
			s.srv.store = store
			tt.setup(store)

			code, _ := s.listAgents("ch-1")
			require.Equal(s.T(), http.StatusInternalServerError, code)
		})
	}
}

func (s *ServerSuite) TestAgentSendChatMessage() {
	tests := []struct {
		name     string
		channel  string
		from     string
		to       string
		noMsgs   bool
		wantChan string
		wantText string
		wantCode int
		wantBody string
	}{
		{name: "terminal agent to the chat", channel: "ch-1", from: "a-0", to: "chat", wantChan: "ch-1", wantText: "[from a-0] hi", wantCode: http.StatusNoContent},
		{name: "chat to a side chat", channel: "ch-1", from: "chat", to: "side-1", wantChan: "side-1", wantText: "[from chat] hi", wantCode: http.StatusNoContent},
		{name: "side chat to the chat", channel: "side-1", from: "chat", to: "chat", wantChan: "ch-1", wantText: "[from side-1] hi", wantCode: http.StatusNoContent},
		{name: "without sender", channel: "ch-1", to: "chat", wantChan: "ch-1", wantText: "hi", wantCode: http.StatusNoContent},
		{name: "chat to itself", channel: "ch-1", from: "chat", to: "chat", wantCode: http.StatusBadRequest, wantBody: "can't message itself"},
		{name: "side chat to itself", channel: "side-1", from: "chat", to: "side-1", wantCode: http.StatusBadRequest, wantBody: "can't message itself"},
		{name: "messages not configured", channel: "ch-1", from: "a-0", to: "chat", noMsgs: true, wantCode: http.StatusConflict, wantBody: "messages are not configured"},
		{name: "another channel's side chat", channel: "ch-1", from: "a-0", to: "side-9", wantCode: http.StatusNotFound, wantBody: "agent side-9 not found in channel ch-1"},
		{name: "not a side chat", channel: "ch-1", from: "a-0", to: "side-x1", wantCode: http.StatusNotFound, wantBody: "not found"},
		{name: "unknown side chat", channel: "ch-1", from: "a-0", to: "side-nope", wantCode: http.StatusNotFound, wantBody: "not found"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.chatStore()
			reg := agentregistry.New()
			s.srv.SetAgentRegistry(reg)
			reg.Register(&agentregistry.AgentInfo{AgentID: "a-0", ChannelID: "ch-1"})
			handler := new(MockIncomingMessageHandler)
			s.srv.msgHandler = handler
			if tt.noMsgs {
				s.srv.msgHandler = nil
			}
			if tt.wantChan != "" {
				handler.On("HandleIncomingMessage", mock.Anything, tt.wantChan, "", tt.wantText, "").Return()
			}

			body, _ := json.Marshal(map[string]string{"channel_id": tt.channel, "from_agent_id": tt.from, "content": "hi"})
			req := httptest.NewRequest("POST", "/api/agents/"+tt.to+"/message", strings.NewReader(string(body)))
			w := httptest.NewRecorder()
			s.mux.ServeHTTP(w, req)

			require.Equal(s.T(), tt.wantCode, w.Code)
			require.Contains(s.T(), w.Body.String(), tt.wantBody)
			handler.AssertExpectations(s.T())
		})
	}
}

func (s *ServerSuite) TestAgentSendChatMessageWithoutStore() {
	s.srv.SetAgentRegistry(agentregistry.New())
	s.srv.store = nil

	req := httptest.NewRequest("POST", "/api/agents/side-1/message", strings.NewReader(`{"channel_id":"ch-1","content":"hi"}`))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusNotFound, w.Code)
}

func (s *ServerSuite) TestAgentSendChatMessageErrors() {
	tests := []struct {
		name  string
		to    string
		setup func(store *MockChannelLister)
	}{
		{name: "sender's channel lookup fails", to: "chat", setup: func(store *MockChannelLister) {
			store.On("GetChannel", mock.Anything, "ch-1").Return(nil, errors.New("db down"))
		}},
		{name: "target lookup fails", to: "side-1", setup: func(store *MockChannelLister) {
			store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1"}, nil)
			store.On("GetChannel", mock.Anything, "side-1").Return(nil, errors.New("db down"))
		}},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.srv.SetAgentRegistry(agentregistry.New())
			store := new(MockChannelLister)
			s.srv.store = store
			tt.setup(store)

			req := httptest.NewRequest("POST", "/api/agents/"+tt.to+"/message", strings.NewReader(`{"channel_id":"ch-1","content":"hi"}`))
			w := httptest.NewRecorder()
			s.mux.ServeHTTP(w, req)
			require.Equal(s.T(), http.StatusInternalServerError, w.Code)
		})
	}
}
