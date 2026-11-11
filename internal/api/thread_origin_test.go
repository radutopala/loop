package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/apiauth"
	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/db"
)

// callAs serves body to handler as the principal p.
func callAs(handler http.HandlerFunc, path, body string, p apiauth.Principal) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", path, strings.NewReader(body))
	req = req.WithContext(apiauth.WithPrincipal(req.Context(), p))
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

// threadStarted makes handler record the tags the new thread's first message
// gets, sending them once HandleThreadCreated runs.
func threadStarted(handler *MockIncomingMessageHandler, threadID, message string) <-chan map[string]string {
	got := make(chan map[string]string, 1)
	handler.On("HandleThreadCreated", mock.Anything, threadID, "", message).
		Run(func(args mock.Arguments) {
			got <- bot.MessageTagsFrom(args.Get(0).(context.Context))
		}).Return().Once()
	return got
}

// A thread's first message names the channel it was created from, as
// send_message's do: the parent's too, since the thread isn't the parent.
func (s *ServerSuite) TestCreateThreadOriginTags() {
	agent := apiauth.Principal{Kind: apiauth.KindAgent, ChannelID: "ch-1"}
	owner := apiauth.Principal{Kind: apiauth.KindOwner}
	tests := []struct {
		name      string
		principal apiauth.Principal
		body      string
		want      map[string]string
	}{
		{
			name:      "agent in the parent",
			principal: agent,
			body:      `{"channel_id":"ch-1","name":"t","message":"go"}`,
			want:      map[string]string{bot.TagOriginChannel: "ch-1", bot.TagOriginChannelName: "backend", bot.TagOriginMessage: "42"},
		},
		{
			name:      "agent token can't claim another channel",
			principal: agent,
			body:      `{"channel_id":"ch-1","name":"t","message":"go","from_channel_id":"forged"}`,
			want:      map[string]string{bot.TagOriginChannel: "ch-1", bot.TagOriginChannelName: "backend", bot.TagOriginMessage: "42"},
		},
		{
			name:      "owner token names its channel",
			principal: owner,
			body:      `{"channel_id":"ch-1","name":"t","message":"go","from_channel_id":"ch-1"}`,
			want:      map[string]string{bot.TagOriginChannel: "ch-1", bot.TagOriginChannelName: "backend", bot.TagOriginMessage: "42"},
		},
		{name: "owner without a channel", principal: owner, body: `{"channel_id":"ch-1","name":"t","message":"go"}`},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			if tc.want != nil {
				s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1", Name: "backend"}, nil).Once()
				s.store.On("LatestPromptRowID", mock.Anything, "ch-1").Return(int64(42), nil).Once()
			}
			s.threads.On("CreateThread", mock.Anything, "ch-1", "t", "", "").Return("thread-1", nil)
			handler := new(MockIncomingMessageHandler)
			s.srv.SetIncomingMessageHandler(handler)
			got := threadStarted(handler, "thread-1", "go")

			rec := callAs(s.srv.handleCreateThread, "/api/threads", tc.body, tc.principal)

			require.Equal(s.T(), http.StatusCreated, rec.Code, rec.Body.String())
			require.Equal(s.T(), tc.want, <-got)
			s.store.AssertExpectations(s.T())
		})
	}
}

// Without the orchestrator the thread stores its first message itself, with
// the origin label the agent would otherwise read from the tags.
func (s *ServerSuite) TestCreateThreadOriginLabelWithoutHandler() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1"}, nil)
	s.store.On("LatestPromptRowID", mock.Anything, "ch-1").Return(int64(0), nil)
	label := "[from channel_id: ch-1; if it needs an answer, send it with send_message to this channel_id] go"
	s.threads.On("CreateThread", mock.Anything, "ch-1", "t", "", label).Return("thread-1", nil)

	rec := callAs(s.srv.handleCreateThread, "/api/threads", `{"channel_id":"ch-1","name":"t","message":"go"}`,
		apiauth.Principal{Kind: apiauth.KindAgent, ChannelID: "ch-1"})

	require.Equal(s.T(), http.StatusCreated, rec.Code, rec.Body.String())
	s.threads.AssertExpectations(s.T())
}

func (s *ServerSuite) TestCreateWorktreeOriginTags() {
	dir := initGitRepo(s.T())
	cmd := exec.Command("git", "branch", "feature/origin")
	cmd.Dir = dir
	require.NoError(s.T(), cmd.Run())

	s.srv.sys = s.sys
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", Name: "backend", DirPath: dir}, nil)
	s.store.On("LatestPromptRowID", mock.Anything, "ch1").Return(int64(7), nil)
	s.threads.On("CreateThread", mock.Anything, "ch1", mock.Anything, "", "").Return("wt-thread", nil)
	s.store.On("GetChannel", mock.Anything, "wt-thread").Return(&db.Channel{ChannelID: "wt-thread", DirPath: dir, ParentID: "ch1"}, nil)
	s.store.On("UpsertChannel", mock.Anything, mock.Anything).Return(nil)
	handler := new(MockIncomingMessageHandler)
	s.srv.SetIncomingMessageHandler(handler)
	got := threadStarted(handler, "wt-thread", "Implement it")

	rec := callAs(s.srv.handleCreateWorktree, "/api/worktrees", `{"channel_id":"ch1","branch":"feature/origin","name":"wt","message":"Implement it"}`,
		apiauth.Principal{Kind: apiauth.KindAgent, ChannelID: "ch1"})

	require.Equal(s.T(), http.StatusCreated, rec.Code, rec.Body.String())
	require.Equal(s.T(), map[string]string{bot.TagOriginChannel: "ch1", bot.TagOriginChannelName: "backend", bot.TagOriginMessage: "7"}, <-got)
}
