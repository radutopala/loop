package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
)

func (s *ServerSuite) serve(method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	return w
}

func (s *ServerSuite) TestCreateLearnProposals() {
	learnCh := &db.Channel{ChannelID: "l-1", ParentID: "ch-1", Kind: db.ChannelKindLearn}
	valid := `{"kind":"rename","title":" Rename it ","rationale":" it's about login ","payload":{"name":"login"}}`
	tests := []struct {
		name      string
		body      string
		noStore   bool
		channel   *db.Channel
		getErr    error
		insertErr error
		hub       bool
		wantCode  int
		wantBody  string
	}{
		{name: "bad json", body: `{`, wantCode: http.StatusBadRequest},
		{name: "no proposals", body: `{"proposals":[]}`, wantCode: http.StatusBadRequest, wantBody: "proposals is required"},
		{
			name:     "too many",
			body:     `{"proposals":[` + strings.TrimSuffix(strings.Repeat(valid+",", 6), ",") + `]}`,
			wantCode: http.StatusBadRequest, wantBody: "at most 5 proposals",
		},
		{name: "no store", body: `{"proposals":[` + valid + `]}`, noStore: true, wantCode: http.StatusNotImplemented},
		{name: "lookup error", body: `{"proposals":[` + valid + `]}`, getErr: errors.New("db down"), wantCode: http.StatusInternalServerError},
		{name: "missing channel", body: `{"proposals":[` + valid + `]}`, wantCode: http.StatusNotFound, wantBody: "not a learn thread"},
		{name: "not a learn thread", body: `{"proposals":[` + valid + `]}`, channel: &db.Channel{ChannelID: "l-1"}, wantCode: http.StatusNotFound},
		{
			name:    "invalid payload",
			body:    `{"proposals":[` + valid + `,{"kind":"mount","title":"m","payload":{"mount":"x"}}]}`,
			channel: learnCh, wantCode: http.StatusBadRequest, wantBody: "proposal 2: mount payload",
		},
		{
			name:    "long rationale",
			body:    `{"proposals":[{"kind":"rename","title":"t","rationale":"` + strings.Repeat("r", 1001) + `","payload":{"name":"x"}}]}`,
			channel: learnCh, wantCode: http.StatusBadRequest, wantBody: "rationale is longer than 1000",
		},
		{name: "insert error", body: `{"proposals":[` + valid + `]}`, channel: learnCh, insertErr: errors.New("disk full"), wantCode: http.StatusInternalServerError},
		{name: "stored", body: `{"proposals":[` + valid + `]}`, channel: learnCh, wantCode: http.StatusCreated},
		{name: "stored and broadcast", body: `{"proposals":[` + valid + `]}`, channel: learnCh, hub: true, wantCode: http.StatusCreated},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			if tc.noStore {
				s.srv.store = nil
			}
			if tc.hub {
				s.srv.eventsHub = NewEventsHub(testLogger())
			}
			s.store.On("GetChannel", mock.Anything, "l-1").Return(tc.channel, tc.getErr)
			s.store.On("InsertLearnProposals", mock.Anything, mock.Anything).Return(tc.insertErr)

			w := s.serve("POST", "/api/channels/l-1/learn/proposals", tc.body)
			require.Equal(s.T(), tc.wantCode, w.Code, w.Body.String())
			require.Contains(s.T(), w.Body.String(), tc.wantBody)
			if tc.wantCode != http.StatusCreated {
				return
			}
			var resp learnProposalsResponse
			require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
			require.Equal(s.T(), []*db.LearnProposal{{
				ChannelID: "ch-1", LearnChannelID: "l-1", Kind: db.LearnKindRename,
				Title: "Rename it", Rationale: "it's about login", Payload: `{"name":"login"}`,
			}}, resp.Proposals)
		})
	}
}

func (s *ServerSuite) TestListLearnProposals() {
	stored := []*db.LearnProposal{{ID: 2, ChannelID: "ch-1", Kind: db.LearnKindRename, Status: db.LearnPending}}
	tests := []struct {
		name     string
		channel  *db.Channel
		list     []*db.LearnProposal
		listErr  error
		wantCode int
		want     []*db.LearnProposal
	}{
		{name: "missing channel", wantCode: http.StatusNotFound},
		{name: "list error", channel: &db.Channel{ChannelID: "ch-1"}, listErr: errors.New("db down"), wantCode: http.StatusInternalServerError},
		{name: "none", channel: &db.Channel{ChannelID: "ch-1"}, wantCode: http.StatusOK, want: []*db.LearnProposal{}},
		{name: "some", channel: &db.Channel{ChannelID: "ch-1"}, list: stored, wantCode: http.StatusOK, want: stored},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(tc.channel, nil)
			s.store.On("ListLearnProposals", mock.Anything, "ch-1").Return(tc.list, tc.listErr)

			w := s.serve("GET", "/api/channels/ch-1/learn/proposals", "")
			require.Equal(s.T(), tc.wantCode, w.Code)
			if tc.wantCode != http.StatusOK {
				return
			}
			var resp learnProposalsResponse
			require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
			require.Equal(s.T(), tc.want, resp.Proposals)
		})
	}
}

func (s *ServerSuite) TestSettleLearnProposalErrors() {
	pending := &db.LearnProposal{ID: 7, ChannelID: "ch-1", Kind: db.LearnKindRename, Payload: `{"name":"x"}`, Status: db.LearnPending}
	tests := []struct {
		name     string
		path     string
		noStore  bool
		proposal *db.LearnProposal
		getErr   error
		claimed  bool
		claimErr error
		setErr   error
		wantCode int
		wantBody string
	}{
		{name: "no store", path: "/api/learn/proposals/7/dismiss", noStore: true, wantCode: http.StatusNotImplemented},
		{name: "bad id", path: "/api/learn/proposals/x/dismiss", wantCode: http.StatusBadRequest, wantBody: "invalid id"},
		{name: "lookup error", path: "/api/learn/proposals/7/dismiss", getErr: errors.New("db down"), wantCode: http.StatusInternalServerError},
		{name: "missing", path: "/api/learn/proposals/7/dismiss", wantCode: http.StatusNotFound},
		{name: "claim error", path: "/api/learn/proposals/7/apply", proposal: pending, claimErr: errors.New("db down"), wantCode: http.StatusInternalServerError},
		{
			name: "already settled", path: "/api/learn/proposals/7/apply",
			proposal: &db.LearnProposal{ID: 7, Status: db.LearnApplied},
			wantCode: http.StatusConflict, wantBody: "proposal is already applied",
		},
		{name: "status error", path: "/api/learn/proposals/7/dismiss", proposal: pending, claimed: true, setErr: errors.New("db down"), wantCode: http.StatusInternalServerError},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			if tc.noStore {
				s.srv.store = nil
			}
			s.store.On("GetLearnProposal", mock.Anything, int64(7)).Return(tc.proposal, tc.getErr)
			s.store.On("ClaimLearnProposal", mock.Anything, int64(7)).Return(tc.claimed, tc.claimErr)
			s.store.On("SetLearnProposalStatus", mock.Anything, int64(7), mock.Anything, mock.Anything).Return(tc.setErr)

			w := s.serve("POST", tc.path, "")
			require.Equal(s.T(), tc.wantCode, w.Code)
			require.Contains(s.T(), w.Body.String(), tc.wantBody)
		})
	}
}

func (s *ServerSuite) TestDismissLearnProposal() {
	s.srv.eventsHub = NewEventsHub(testLogger())
	s.store.On("GetLearnProposal", mock.Anything, int64(7)).Return(&db.LearnProposal{ID: 7, ChannelID: "ch-1", Status: db.LearnFailed, Error: "old"}, nil)
	s.store.On("ClaimLearnProposal", mock.Anything, int64(7)).Return(true, nil)
	s.store.On("SetLearnProposalStatus", mock.Anything, int64(7), db.LearnDismissed, "").Return(nil)

	w := s.serve("POST", "/api/learn/proposals/7/dismiss", "")
	require.Equal(s.T(), http.StatusOK, w.Code)
	var got db.LearnProposal
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &got))
	require.Equal(s.T(), db.LearnDismissed, got.Status)
	require.Empty(s.T(), got.Error)
}

// applyProposal runs the apply endpoint on a claimable proposal and returns
// the status and error it was settled with.
func (s *ServerSuite) applyProposal(kind, payload string) (string, string) {
	s.store.On("GetLearnProposal", mock.Anything, int64(7)).Return(&db.LearnProposal{ID: 7, ChannelID: "ch-1", Kind: kind, Payload: payload, Status: db.LearnPending}, nil)
	s.store.On("ClaimLearnProposal", mock.Anything, int64(7)).Return(true, nil)
	var status, errText string
	s.store.On("SetLearnProposalStatus", mock.Anything, int64(7), mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { status, errText = args.String(2), args.String(3) }).Return(nil)

	w := s.serve("POST", "/api/learn/proposals/7/apply", "")
	require.Equal(s.T(), http.StatusOK, w.Code)
	var got db.LearnProposal
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &got))
	require.Equal(s.T(), status, got.Status)
	require.Equal(s.T(), errText, got.Error)
	return status, errText
}

func (s *ServerSuite) TestApplyLearnProposalChannelKinds() {
	tests := []struct {
		name    string
		kind    string
		payload string
		channel *db.Channel
		getErr  error
		setup   func()
		hub     bool
		wantErr string
	}{
		{name: "undecodable", kind: db.LearnKindRename, payload: `{"nope":1}`, wantErr: "unknown field"},
		{name: "lookup error", kind: db.LearnKindRename, payload: `{"name":"x"}`, getErr: errors.New("db down"), wantErr: "db down"},
		{name: "channel gone", kind: db.LearnKindRename, payload: `{"name":"x"}`, wantErr: "channel not found"},
		{
			name: "rename", kind: db.LearnKindRename, payload: `{"name":" login bug "}`, channel: &db.Channel{ChannelID: "ch-1"},
			setup: func() { s.store.On("UpdateChannelName", mock.Anything, "ch-1", "login bug").Return(nil) },
		},
		{
			name: "rename broadcast", kind: db.LearnKindRename, payload: `{"name":"login bug"}`, channel: &db.Channel{ChannelID: "ch-1"}, hub: true,
			setup: func() { s.store.On("UpdateChannelName", mock.Anything, "ch-1", "login bug").Return(nil) },
		},
		{
			name: "rename error", kind: db.LearnKindRename, payload: `{"name":"x"}`, channel: &db.Channel{ChannelID: "ch-1"},
			setup:   func() { s.store.On("UpdateChannelName", mock.Anything, "ch-1", "x").Return(errors.New("locked")) },
			wantErr: "locked",
		},
		{
			name: "description", kind: db.LearnKindDescription, payload: `{"description":" chasing it "}`, channel: &db.Channel{ChannelID: "ch-1"},
			setup: func() { s.store.On("UpdateChannelDescription", mock.Anything, "ch-1", "chasing it").Return(nil) },
		},
		{
			name: "description broadcast", kind: db.LearnKindDescription, payload: `{"description":"d"}`, channel: &db.Channel{ChannelID: "ch-1"}, hub: true,
			setup: func() { s.store.On("UpdateChannelDescription", mock.Anything, "ch-1", "d").Return(nil) },
		},
		{
			name: "description error", kind: db.LearnKindDescription, payload: `{"description":"d"}`, channel: &db.Channel{ChannelID: "ch-1"},
			setup: func() {
				s.store.On("UpdateChannelDescription", mock.Anything, "ch-1", "d").Return(errors.New("locked"))
			},
			wantErr: "locked",
		},
		{
			name: "ticket url", kind: db.LearnKindTicketURL, payload: `{"ticket_url":" https://tracker.example.com/T-1 "}`, channel: &db.Channel{ChannelID: "ch-1"},
			setup: func() {
				s.store.On("UpdateChannelTicketURL", mock.Anything, "ch-1", "https://tracker.example.com/T-1").Return(nil)
			},
		},
		{
			name: "ticket url broadcast", kind: db.LearnKindTicketURL, payload: `{"ticket_url":"https://tracker.example.com/T-1"}`, channel: &db.Channel{ChannelID: "ch-1"}, hub: true,
			setup: func() {
				s.store.On("UpdateChannelTicketURL", mock.Anything, "ch-1", "https://tracker.example.com/T-1").Return(nil)
			},
		},
		{
			name: "ticket url error", kind: db.LearnKindTicketURL, payload: `{"ticket_url":"https://tracker.example.com/T-1"}`, channel: &db.Channel{ChannelID: "ch-1"},
			setup: func() {
				s.store.On("UpdateChannelTicketURL", mock.Anything, "ch-1", mock.Anything).Return(errors.New("locked"))
			},
			wantErr: "locked",
		},
		{
			name: "task", kind: db.LearnKindScheduledTask, payload: `{"type":"cron","schedule":"0 9 * * *","prompt":"p"}`,
			channel: &db.Channel{ChannelID: "ch-1"},
			setup: func() {
				s.scheduler.On("AddTask", mock.Anything, &db.ScheduledTask{
					ChannelID: "ch-1", Schedule: "0 9 * * *", Type: db.TaskTypeCron, Prompt: "p", Enabled: true,
				}).Return(int64(3), nil)
			},
		},
		{
			name: "task broadcast", kind: db.LearnKindScheduledTask, payload: `{"type":"interval","schedule":"1h","bash_script":"b","auto_delete_sec":5}`,
			channel: &db.Channel{ChannelID: "ch-1"}, hub: true,
			setup: func() {
				s.scheduler.On("AddTask", mock.Anything, &db.ScheduledTask{
					ChannelID: "ch-1", Schedule: "1h", Type: db.TaskTypeInterval, BashScript: "b", AutoDeleteSec: 5, Enabled: true,
				}).Return(int64(3), nil)
			},
		},
		{
			name: "task error", kind: db.LearnKindScheduledTask, payload: `{"type":"cron","schedule":"bad","prompt":"p"}`,
			channel: &db.Channel{ChannelID: "ch-1"},
			setup: func() {
				s.scheduler.On("AddTask", mock.Anything, mock.Anything).Return(int64(0), errors.New("invalid cron"))
			},
			wantErr: "invalid cron",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			if tc.hub {
				s.srv.eventsHub = NewEventsHub(testLogger())
			}
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(tc.channel, tc.getErr)
			if tc.setup != nil {
				tc.setup()
			}
			status, errText := s.applyProposal(tc.kind, tc.payload)
			if tc.wantErr != "" {
				require.Equal(s.T(), db.LearnFailed, status)
				require.Contains(s.T(), errText, tc.wantErr)
				return
			}
			require.Equal(s.T(), db.LearnApplied, status)
			require.Empty(s.T(), errText)
		})
	}
}

func (s *ServerSuite) TestApplyLearnProposalConfigKinds() {
	global := &config.Config{
		PromptShortcuts: []config.PromptShortcut{{Name: "review"}},
		BashShortcuts:   []config.BashShortcut{{Name: "lint"}},
		Mounts:          []string{"~/.gitconfig:~/.gitconfig:ro"},
	}
	tests := []struct {
		name    string
		kind    string
		payload string
		initial string // "" = no project config
		want    string
		wantErr string
	}{
		{
			name: "prompt shortcut", kind: db.LearnKindPromptShortcut, payload: `{"name":"fix","prompt":"fix it"}`,
			initial: "{\n  // mine\n  \"prompt_shortcuts\": []\n}\n",
			want:    "{\n  // mine\n  \"prompt_shortcuts\": [\n    {\n      \"name\": \"fix\",\n      \"prompt\": \"fix it\"\n    }\n  ]\n}\n",
		},
		{name: "prompt shortcut exists", kind: db.LearnKindPromptShortcut, payload: `{"name":"review","prompt":"p"}`, wantErr: `prompt shortcut named "review" already exists`},
		{
			name: "bash shortcut", kind: db.LearnKindBashShortcut, payload: `{"name":"test","command":"make test"}`,
			want: "{\n  \"bash_shortcuts\": [\n    {\n      \"name\": \"test\",\n      \"command\": \"make test\"\n    }\n  ]\n}\n",
		},
		{name: "bash shortcut exists", kind: db.LearnKindBashShortcut, payload: `{"name":"lint","command":"c"}`, wantErr: `bash shortcut named "lint" already exists`},
		{
			name: "gate rule", kind: db.LearnKindGateRule, payload: `{"type":"command","rule":{"commands":["git"],"decision":"approve"}}`,
			want: "{\n  \"gates\": {\n    \"agentgate\": {\n      \"command_rules\": [\n        {\n          \"commands\": [\n            \"git\"\n          ],\n          \"decision\": \"approve\"\n        }\n      ]\n    }\n  }\n}\n",
		},
		{
			name: "first mount keeps the global ones", kind: db.LearnKindMount, payload: `{"mount":"~/.aws:~/.aws:ro"}`,
			want: "{\n  \"mounts\": [\n    \"~/.gitconfig:~/.gitconfig:ro\",\n    \"~/.aws:~/.aws:ro\"\n  ]\n}\n",
		},
		{name: "mount exists", kind: db.LearnKindMount, payload: `{"mount":"~/.gitconfig:~/.gitconfig:ro"}`, wantErr: "already exists"},
		{name: "broken config", kind: db.LearnKindMount, payload: `{"mount":"a:b"}`, initial: "{", wantErr: "parsing"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			dir := s.T().TempDir()
			path := filepath.Join(dir, ".loop", "config.json")
			if tc.initial != "" {
				require.NoError(s.T(), os.MkdirAll(filepath.Dir(path), 0o755))
				require.NoError(s.T(), os.WriteFile(path, []byte(tc.initial), 0o644))
			}
			s.srv.configs.load = func() (*config.Config, error) { return global, nil }
			s.srv.configs.loadProject = func(string, *config.Config) (*config.Config, error) { return global, nil }
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1", DirPath: dir}, nil)

			status, errText := s.applyProposal(tc.kind, tc.payload)
			if tc.wantErr != "" {
				require.Equal(s.T(), db.LearnFailed, status)
				require.Contains(s.T(), errText, tc.wantErr)
				return
			}
			require.Equal(s.T(), db.LearnApplied, status, errText)
			data, err := os.ReadFile(path)
			require.NoError(s.T(), err)
			require.Equal(s.T(), tc.want, string(data))
		})
	}
}

func (s *ServerSuite) TestApplyLearnProposalConfigErrors() {
	s.Run("no project dir", func() {
		s.SetupTest()
		s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1"}, nil)
		status, errText := s.applyProposal(db.LearnKindMount, `{"mount":"a:b"}`)
		require.Equal(s.T(), db.LearnFailed, status)
		require.Contains(s.T(), errText, "has no dir_path")
	})
	s.Run("config load fails", func() {
		s.SetupTest()
		s.srv.configs.load = func() (*config.Config, error) { return nil, os.ErrPermission }
		s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1", DirPath: s.T().TempDir()}, nil)
		status, errText := s.applyProposal(db.LearnKindMount, `{"mount":"a:b"}`)
		require.Equal(s.T(), db.LearnFailed, status)
		require.Equal(s.T(), "loading config failed", errText)
	})
	s.Run("global config gone before seeding", func() {
		s.SetupTest()
		dir := s.T().TempDir()
		loads := 0
		s.srv.configs.load = func() (*config.Config, error) {
			loads++
			if loads > 1 {
				return nil, os.ErrPermission
			}
			return &config.Config{}, nil
		}
		s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1", DirPath: dir}, nil)
		status, errText := s.applyProposal(db.LearnKindMount, `{"mount":"a:b"}`)
		require.Equal(s.T(), db.LearnApplied, status, errText)
		data, err := os.ReadFile(filepath.Join(dir, ".loop", "config.json"))
		require.NoError(s.T(), err)
		require.Equal(s.T(), "{\n  \"mounts\": [\n    \"a:b\"\n  ]\n}\n", string(data))
	})
}
