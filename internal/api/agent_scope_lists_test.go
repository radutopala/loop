package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
)

// An agent can't fetch another project's task or workflow run by id, so a
// listing without a channel_id can't show it one either.
func (s *AgentScopeSuite) TestListingsHoldToTheProject() {
	s.store.On("ListAllScheduledTasks", mock.Anything).Return([]*db.ScheduledTask{
		{ID: 5, ChannelID: "ch1", Prompt: "own"},
		{ID: 8, ChannelID: "ch2", Prompt: "same project"},
		{ID: 6, ChannelID: "ch3", Prompt: "other project"},
		{ID: 9, ChannelID: "ch3", Prompt: "other project again"},
	}, nil)
	s.store.On("ListChannels", mock.Anything).Return([]*db.Channel{
		{ChannelID: "ch1", DirPath: "/proj"}, {ChannelID: "ch2", DirPath: "/proj"}, {ChannelID: "ch3", DirPath: "/other"},
	}, nil)
	s.workflows.On("ListRuns", mock.Anything, "", 50, 0).Return([]*db.WorkflowRun{
		{ID: "r1", ChannelID: "ch2"}, {ID: "r3", ChannelID: "ch3"},
	}, nil)
	h := s.srv.auth.Wrap(s.srv.agentGuard(s.srv.buildMux()))

	tests := []struct {
		name   string
		tok    string
		target string
		want   []string
	}{
		{"agent's tasks", "agent", "/api/tasks", []string{"5", "8"}},
		{"owner's tasks", "owner", "/api/tasks", []string{"5", "8", "6", "9"}},
		{"agent's workflow runs", "agent", "/api/workflows/runs", []string{"r1"}},
		{"owner's workflow runs", "owner", "/api/workflows/runs", []string{"r1", "r3"}},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			req := httptest.NewRequest(http.MethodGet, tc.target, nil)
			req.Header.Set("Authorization", "Bearer "+tc.tok)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			require.Equal(s.T(), http.StatusOK, rec.Code, rec.Body.String())
			var items []struct {
				ID any `json:"id"`
			}
			require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &items))
			got := make([]string, 0, len(items))
			for _, it := range items {
				got = append(got, fmt.Sprint(it.ID))
			}
			require.Equal(s.T(), tc.want, got)
		})
	}
}
