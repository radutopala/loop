package api

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/learn"
	"github.com/radutopala/loop/internal/types"
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
	stale := &db.LearnProposal{ID: 3, ChannelID: "ch-1", Kind: db.LearnKindRename, Status: db.LearnWithdrawn, WithdrawnReason: "stale"}
	withdraw := func(n int) string {
		items := make([]string, n)
		for i := range items {
			items[i] = fmt.Sprintf(`{"id":%d,"reason":"stale"}`, i+1)
		}
		return `"withdraw":[` + strings.Join(items, ",") + `]`
	}
	tests := []struct {
		name         string
		body         string
		noStore      bool
		channel      *db.Channel
		getErr       error
		pass         *db.LearnPass
		passErr      error
		withdrawn    []*db.LearnProposal
		insertErr    error
		hub          bool
		wantCode     int
		wantBody     string
		wantWithdraw []db.LearnWithdrawal
		wantFiled    bool
	}{
		{name: "bad json", body: `{`, wantCode: http.StatusBadRequest},
		{name: "nothing to do", body: `{"proposals":[],"withdraw":[]}`, wantCode: http.StatusBadRequest, wantBody: "proposals or withdraw is required"},
		{
			name:     "too many",
			body:     `{"proposals":[` + strings.TrimSuffix(strings.Repeat(valid+",", 6), ",") + `]}`,
			wantCode: http.StatusBadRequest, wantBody: "at most 5 proposals",
		},
		{name: "too many withdrawals", body: `{` + withdraw(21) + `}`, wantCode: http.StatusBadRequest, wantBody: "at most 20 withdrawals"},
		{name: "no reason", body: `{"withdraw":[{"id":3,"reason":" "}]}`, wantCode: http.StatusBadRequest, wantBody: "withdraw 1: reason is required"},
		{
			name:     "withdrawn twice",
			body:     `{"withdraw":[{"id":3,"reason":"a"},{"id":3,"reason":"b"}]}`,
			wantCode: http.StatusBadRequest, wantBody: "withdraw 2: proposal 3 is withdrawn twice",
		},
		{
			name:     "replaces one withdrawn",
			body:     `{"proposals":[{"kind":"rename","title":"t","payload":{"name":"x"},"replaces":3}],"withdraw":[{"id":3,"reason":"a"}]}`,
			wantCode: http.StatusBadRequest, wantBody: "proposal 1: replaces: proposal 3 is withdrawn twice",
		},
		{
			name:    "not open",
			body:    `{"withdraw":[{"id":3,"reason":"stale"}]}`,
			channel: learnCh, insertErr: &db.LearnWithdrawError{ID: 3, Status: db.LearnApplying},
			wantCode: http.StatusConflict, wantBody: "proposal 3 is applying; only pending or failed proposals can be withdrawn",
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
		{name: "pass lookup error", body: `{"proposals":[` + valid + `]}`, channel: learnCh, passErr: errors.New("db down"), wantCode: http.StatusInternalServerError},
		{
			name: "stored on the pass's turn", body: `{"proposals":[` + valid + `]}`, channel: learnCh,
			pass: &db.LearnPass{ID: 7, MessageID: "b-1"}, wantCode: http.StatusCreated, wantFiled: true,
		},
		{name: "insert error", body: `{"proposals":[` + valid + `]}`, channel: learnCh, insertErr: errors.New("disk full"), wantCode: http.StatusInternalServerError},
		{name: "stored", body: `{"proposals":[` + valid + `]}`, channel: learnCh, wantCode: http.StatusCreated, wantFiled: true},
		{name: "stored and broadcast", body: `{"proposals":[` + valid + `]}`, channel: learnCh, hub: true, wantCode: http.StatusCreated, wantFiled: true},
		{
			name:    "withdraw only",
			body:    `{"withdraw":[{"id":3,"reason":"  stale  "}]}`,
			channel: learnCh, withdrawn: []*db.LearnProposal{stale}, hub: true, wantCode: http.StatusCreated,
			wantWithdraw: []db.LearnWithdrawal{{ID: 3, Reason: "stale"}},
		},
		{
			name:    "replaces and withdraws",
			body:    `{"proposals":[` + strings.TrimSuffix(valid, "}") + `,"replaces":4}],"withdraw":[{"id":3,"reason":"stale"}]}`,
			channel: learnCh, withdrawn: []*db.LearnProposal{stale}, wantCode: http.StatusCreated, wantFiled: true,
			wantWithdraw: []db.LearnWithdrawal{{ID: 3, Reason: "stale"}, {ID: 4, Reason: learn.ReplacedReason}},
		},
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
			s.store.On("LatestLearnPass", mock.Anything, "l-1").Return(tc.pass, tc.passErr)
			s.store.On("FileLearnProposals", mock.Anything, "ch-1", mock.Anything, mock.Anything).Return(tc.withdrawn, tc.insertErr)

			w := s.serve("POST", "/api/channels/l-1/learn/proposals", tc.body)
			require.Equal(s.T(), tc.wantCode, w.Code, w.Body.String())
			require.Contains(s.T(), w.Body.String(), tc.wantBody)
			if tc.wantCode != http.StatusCreated {
				return
			}
			s.store.AssertCalled(s.T(), "FileLearnProposals", mock.Anything, "ch-1", mock.Anything, tc.wantWithdraw)
			var resp learnProposalsResponse
			require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
			want := []*db.LearnProposal{}
			if tc.wantFiled {
				want = []*db.LearnProposal{{
					ChannelID: "ch-1", LearnChannelID: "l-1", Kind: db.LearnKindRename,
					Title: "Rename it", Rationale: "it's about login", Payload: `{"name":"login"}`,
				}}
				if tc.pass != nil {
					want[0].MessageID = tc.pass.MessageID
				}
			}
			require.Equal(s.T(), want, resp.Proposals)
			require.Len(s.T(), resp.Withdrawn, len(tc.withdrawn))
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

func (s *ServerSuite) TestListLearnPasses() {
	stored := []*db.LearnPass{{ID: 2, ChannelID: "ch-1", MessageID: "b-1", LearnChannelID: "l-1", Status: db.LearnPassDone, MessageRowID: 9}}
	tests := []struct {
		name     string
		channel  *db.Channel
		list     []*db.LearnPass
		listErr  error
		wantCode int
		wantBody string
	}{
		{name: "missing channel", wantCode: http.StatusNotFound},
		{name: "list error", channel: &db.Channel{ChannelID: "ch-1"}, listErr: errors.New("db down"), wantCode: http.StatusInternalServerError},
		{name: "none", channel: &db.Channel{ChannelID: "ch-1"}, wantCode: http.StatusOK, wantBody: `{"passes":[]}`},
		{
			name: "some", channel: &db.Channel{ChannelID: "ch-1"}, list: stored, wantCode: http.StatusOK,
			wantBody: `{"passes":[{"id":2,"channel_id":"ch-1","message_id":"b-1","learn_channel_id":"l-1","status":"done",` +
				`"created_at":"0001-01-01T00:00:00Z","updated_at":"0001-01-01T00:00:00Z","message_row_id":9}]}`,
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(tc.channel, nil)
			s.store.On("ListLearnPasses", mock.Anything, "ch-1").Return(tc.list, tc.listErr)

			w := s.serve("GET", "/api/channels/ch-1/learn/passes", "")
			require.Equal(s.T(), tc.wantCode, w.Code)
			if tc.wantCode == http.StatusOK {
				require.JSONEq(s.T(), tc.wantBody, w.Body.String())
			}
		})
	}
}

type MockLearnTurner struct {
	mock.Mock
}

func (m *MockLearnTurner) LearnTurn(ctx context.Context, ch *db.Channel, messageID string) (*db.LearnPass, error) {
	args := m.Called(ctx, ch, messageID)
	p, _ := args.Get(0).(*db.LearnPass)
	return p, args.Error(1)
}

func (s *ServerSuite) TestLearnTurnNotConfigured() {
	require.Equal(s.T(), http.StatusNotImplemented, s.serve("POST", "/api/channels/ch-1/learn/passes", `{"message_id":"b-1"}`).Code)
}

func (s *ServerSuite) TestLearnTurn() {
	ch := &db.Channel{ChannelID: "ch-1", Platform: types.PlatformLocal}
	tests := []struct {
		name     string
		body     string
		channel  string
		result   *db.LearnPass
		err      error
		wantCode int
		wantBody string
	}{
		{name: "bad body", body: `{`, wantCode: http.StatusBadRequest},
		{name: "missing message id", body: `{}`, wantCode: http.StatusBadRequest, wantBody: "message_id is required"},
		{name: "channel gone", body: `{"message_id":"b-1"}`, channel: "gone", wantCode: http.StatusNotFound},
		{
			name: "queues", body: `{"message_id":"b-1"}`,
			result:   &db.LearnPass{ID: 3, ChannelID: "ch-1", MessageID: "b-1", LearnChannelID: "l-1", Status: db.LearnPassQueued},
			wantCode: http.StatusOK,
			wantBody: `{"id":3,"channel_id":"ch-1","message_id":"b-1","learn_channel_id":"l-1","status":"queued",` +
				`"created_at":"0001-01-01T00:00:00Z","updated_at":"0001-01-01T00:00:00Z"}`,
		},
		{
			name: "already running", body: `{"message_id":"b-1"}`,
			result:   &db.LearnPass{ID: 2, ChannelID: "ch-1", MessageID: "b-1", LearnChannelID: "l-1", Status: db.LearnPassRunning},
			wantCode: http.StatusOK,
			wantBody: `{"id":2,"channel_id":"ch-1","message_id":"b-1","learn_channel_id":"l-1","status":"running",` +
				`"created_at":"0001-01-01T00:00:00Z","updated_at":"0001-01-01T00:00:00Z"}`,
		},
		{name: "unavailable", body: `{"message_id":"b-1"}`, err: learn.ErrUnavailable, wantCode: http.StatusBadRequest, wantBody: learn.ErrUnavailable.Error()},
		{name: "not a turn", body: `{"message_id":"b-1"}`, err: learn.ErrNotATurn, wantCode: http.StatusBadRequest, wantBody: learn.ErrNotATurn.Error()},
		{name: "no session", body: `{"message_id":"b-1"}`, err: learn.ErrNoSession, wantCode: http.StatusConflict, wantBody: learn.ErrNoSession.Error()},
		{name: "parent gone", body: `{"message_id":"b-1"}`, err: fmt.Errorf("thread: %w", db.ErrParentGone), wantCode: http.StatusNotFound, wantBody: "channel not found"},
		{name: "store error", body: `{"message_id":"b-1"}`, err: errors.New("disk full"), wantCode: http.StatusInternalServerError, wantBody: "disk full"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			turner := new(MockLearnTurner)
			s.srv.SetLearnTurner(turner)
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(ch, nil).Maybe()
			s.store.On("GetChannel", mock.Anything, "gone").Return(nil, nil).Maybe()
			turner.On("LearnTurn", mock.Anything, ch, "b-1").Return(tc.result, tc.err).Maybe()

			channel := cmp.Or(tc.channel, "ch-1")
			w := s.serve("POST", "/api/channels/"+channel+"/learn/passes", tc.body)
			require.Equal(s.T(), tc.wantCode, w.Code, w.Body.String())
			if tc.wantCode == http.StatusOK {
				require.JSONEq(s.T(), tc.wantBody, w.Body.String())
			} else if tc.wantBody != "" {
				require.Contains(s.T(), w.Body.String(), tc.wantBody)
			}
			if tc.result != nil || tc.err != nil {
				turner.AssertExpectations(s.T())
			}
		})
	}
}

func (s *ServerSuite) TestSettleLearnProposalErrors() {
	pending := &db.LearnProposal{ID: 7, ChannelID: "ch-1", Kind: db.LearnKindRename, Payload: `{"name":"x"}`, Status: db.LearnPending}
	tests := []struct {
		name      string
		path      string
		noStore   bool
		proposal  *db.LearnProposal
		getErr    error
		reread    *db.LearnProposal
		rereadErr error
		claimed   bool
		claimErr  error
		setErr    error
		wantCode  int
		wantBody  string
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
		{
			name: "withdrawn meanwhile", path: "/api/learn/proposals/7/dismiss",
			proposal: pending, reread: &db.LearnProposal{ID: 7, Status: db.LearnWithdrawn},
			wantCode: http.StatusConflict, wantBody: "proposal is already withdrawn",
		},
		{
			name: "reread error", path: "/api/learn/proposals/7/apply",
			proposal: pending, rereadErr: errors.New("db down"),
			wantCode: http.StatusConflict, wantBody: "proposal is already pending",
		},
		{name: "status error", path: "/api/learn/proposals/7/dismiss", proposal: pending, claimed: true, setErr: errors.New("db down"), wantCode: http.StatusInternalServerError},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			if tc.noStore {
				s.srv.store = nil
			}
			s.store.On("GetLearnProposal", mock.Anything, int64(7)).Return(tc.proposal, tc.getErr).Once()
			if tc.reread != nil || tc.rereadErr != nil {
				s.store.On("GetLearnProposal", mock.Anything, int64(7)).Return(tc.reread, tc.rereadErr).Once()
			} else {
				s.store.On("GetLearnProposal", mock.Anything, int64(7)).Return(tc.proposal, tc.getErr)
			}
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

// TestSettleLearnProposalOutlivesClient checks a claimed proposal is applied
// and settled even when the client has gone away: its request context is
// cancelled, but the store and the apply never see that.
func (s *ServerSuite) TestSettleLearnProposalOutlivesClient() {
	live := mock.MatchedBy(func(ctx context.Context) bool { return ctx.Err() == nil })
	for _, action := range []string{"apply", "dismiss"} {
		s.Run(action, func() {
			s.SetupTest()
			s.store.On("GetLearnProposal", live, int64(7)).Return(&db.LearnProposal{ID: 7, ChannelID: "ch-1", Kind: db.LearnKindRename, Payload: `{"name":"x"}`, Status: db.LearnPending}, nil)
			s.store.On("ClaimLearnProposal", live, int64(7)).Return(true, nil)
			s.store.On("GetChannel", live, "ch-1").Return(&db.Channel{ChannelID: "ch-1"}, nil).Maybe()
			s.store.On("UpdateChannelName", live, "ch-1", "x").Return(nil).Maybe()
			s.store.On("SetLearnProposalStatus", live, int64(7), mock.Anything, "").Return(nil)

			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			req := httptest.NewRequest("POST", "/api/learn/proposals/7/"+action, nil).WithContext(ctx)
			w := httptest.NewRecorder()
			s.mux.ServeHTTP(w, req)
			require.Equal(s.T(), http.StatusOK, w.Code)
			s.store.AssertExpectations(s.T())
		})
	}
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
			name: "task error", kind: db.LearnKindScheduledTask, payload: `{"type":"cron","schedule":"0 9 * * *","prompt":"p"}`,
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
		Gates: config.GatesConfig{Agentgate: config.AgentgateConfig{
			PathRules:    []types.PathRule{{Pattern: "/etc/**", Decision: "deny"}},
			CommandRules: []types.CommandRule{{Commands: []string{"rm"}, Decision: "approve"}},
			FileRules:    []types.FileRule{{Paths: []string{"/secret/**"}, Decision: "deny"}},
		}},
	}
	tests := []struct {
		name    string
		kind    string
		payload string
		initial string // "" = no project config
		want    string // "" = no project config written
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
		{name: "path rule exists", kind: db.LearnKindGateRule, payload: `{"type":"path","rule":{"pattern":"/etc/**","decision":"deny"}}`},
		{name: "command rule exists", kind: db.LearnKindGateRule, payload: `{"type":"command","rule":{"commands":["rm"],"decision":"approve"}}`},
		{name: "file rule exists", kind: db.LearnKindGateRule, payload: `{"type":"file","rule":{"paths":["/secret/**"],"decision":"deny"}}`},
		{
			// Project mounts add to the global ones, so the first starts the list alone.
			name: "first mount", kind: db.LearnKindMount, payload: `{"mount":"~/.aws:~/.aws:ro"}`,
			want: "{\n  \"mounts\": [\n    \"~/.aws:~/.aws:ro\"\n  ]\n}\n",
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
			if tc.want == "" {
				require.NoFileExists(s.T(), path)
				return
			}
			data, err := os.ReadFile(path)
			require.NoError(s.T(), err)
			require.Equal(s.T(), tc.want, string(data))
		})
	}
}

func (s *ServerSuite) TestApplyLearnProposalRelativeMountExists() {
	dir := s.T().TempDir()
	// The merged project config has its relative mounts resolved against
	// the project dir, so the proposal's relative mount must match that.
	merged := &config.Config{Mounts: []string{filepath.Join(dir, "data") + ":/data"}}
	s.srv.configs.load = func() (*config.Config, error) { return &config.Config{}, nil }
	s.srv.configs.loadProject = func(string, *config.Config) (*config.Config, error) { return merged, nil }
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1", DirPath: dir}, nil)

	status, errText := s.applyProposal(db.LearnKindMount, `{"mount":"./data:/data"}`)
	require.Equal(s.T(), db.LearnFailed, status)
	require.Contains(s.T(), errText, `mount "./data:/data" already exists`)
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
}

// TestPreviewLearnProposal shows the edit apply would make as a diff, and
// leaves the config file as it was.
func (s *ServerSuite) TestPreviewLearnProposal() {
	global := &config.Config{PromptShortcuts: []config.PromptShortcut{{Name: "review"}}, Gates: config.GatesConfig{Agentgate: config.AgentgateConfig{
		CommandRules: []types.CommandRule{{Commands: []string{"rm"}, Decision: "approve"}},
	}}}
	tests := []struct {
		name     string
		kind     string
		payload  string
		initial  string // "" = no project config
		channel  *db.Channel
		wantDiff string
		wantErr  string
		wantPath bool
	}{
		{
			name: "edit", kind: db.LearnKindMount, payload: `{"mount":"~/.aws:~/.aws:ro"}`,
			initial:  "{\n  // mine\n  \"envs\": {}\n}\n",
			wantDiff: "--- PATH\n+++ PATH\n@@ -1,4 +1,7 @@\n {\n   // mine\n-  \"envs\": {}\n+  \"envs\": {},\n+  \"mounts\": [\n+    \"~/.aws:~/.aws:ro\"\n+  ]\n }\n",
			wantPath: true,
		},
		{
			name: "new file", kind: db.LearnKindBashShortcut, payload: `{"name":"test","command":"make test"}`,
			wantDiff: "--- /dev/null\n+++ PATH\n@@ -0,0 +1,8 @@\n+{\n+  \"bash_shortcuts\": [\n+    {\n+      \"name\": \"test\",\n+      \"command\": \"make test\"\n+    }\n+  ]\n+}\n",
			wantPath: true,
		},
		{name: "already there", kind: db.LearnKindGateRule, payload: `{"type":"command","rule":{"commands":["rm"],"decision":"approve"}}`, wantPath: true},
		{name: "apply would fail", kind: db.LearnKindPromptShortcut, payload: `{"name":"review","prompt":"p"}`, wantErr: `prompt shortcut named "review" already exists`, wantPath: true},
		{name: "no file edit", kind: db.LearnKindRename, payload: `{"name":"x"}`},
		{name: "bad payload", kind: db.LearnKindMount, payload: `{`, wantErr: "payload"},
		{name: "channel gone", kind: db.LearnKindMount, payload: `{"mount":"a:b"}`, channel: &db.Channel{}, wantErr: "channel not found"},
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
			ch := &db.Channel{ChannelID: "ch-1", DirPath: dir}
			if tc.channel != nil {
				ch = nil
			}
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(ch, nil)
			s.store.On("GetLearnProposal", mock.Anything, int64(7)).Return(&db.LearnProposal{ID: 7, ChannelID: "ch-1", Kind: tc.kind, Payload: tc.payload, Status: db.LearnPending}, nil)

			w := s.serve("GET", "/api/learn/proposals/7/preview", "")
			require.Equal(s.T(), http.StatusOK, w.Code, w.Body.String())
			var got learnPreviewResponse
			require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &got))
			wantPath := ""
			if tc.wantPath {
				wantPath = path
			}
			require.Equal(s.T(), wantPath, got.Path)
			require.Equal(s.T(), strings.ReplaceAll(tc.wantDiff, "PATH", path), got.Diff)
			if tc.wantErr == "" {
				require.Empty(s.T(), got.Error)
			} else {
				require.Contains(s.T(), got.Error, tc.wantErr)
			}
			if tc.initial == "" {
				require.NoFileExists(s.T(), path, "the preview writes nothing")
			} else {
				data, err := os.ReadFile(path)
				require.NoError(s.T(), err)
				require.Equal(s.T(), tc.initial, string(data), "the preview writes nothing")
			}
		})
	}
}

func (s *ServerSuite) TestPreviewLearnProposalErrors() {
	tests := []struct {
		name     string
		path     string
		noStore  bool
		proposal *db.LearnProposal
		getErr   error
		wantCode int
	}{
		{name: "no store", path: "/api/learn/proposals/7/preview", noStore: true, wantCode: http.StatusNotImplemented},
		{name: "bad id", path: "/api/learn/proposals/x/preview", wantCode: http.StatusBadRequest},
		{name: "lookup error", path: "/api/learn/proposals/7/preview", getErr: errors.New("db down"), wantCode: http.StatusInternalServerError},
		{name: "missing", path: "/api/learn/proposals/7/preview", wantCode: http.StatusNotFound},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			if tc.noStore {
				s.srv.store = nil
			}
			s.store.On("GetLearnProposal", mock.Anything, int64(7)).Return(tc.proposal, tc.getErr)
			w := s.serve("GET", tc.path, "")
			require.Equal(s.T(), tc.wantCode, w.Code)
		})
	}
}

// gatedReadSystem holds every read of path until release is closed,
// reporting each one on reads first.
type gatedReadSystem struct {
	serverSystem
	path    string
	reads   chan struct{}
	release chan struct{}
}

func (g *gatedReadSystem) ReadFile(name string) ([]byte, error) {
	if name == g.path {
		g.reads <- struct{}{}
		<-g.release
	}
	return g.serverSystem.ReadFile(name)
}

// TestApplyLearnConfigConcurrent applies two proposals to the same config
// at once, as Apply all does: the second edit waits for the first to be
// written, so both entries land.
func (s *ServerSuite) TestApplyLearnConfigConcurrent() {
	dir := s.T().TempDir()
	path := filepath.Join(dir, ".loop", "config.json")
	sys := &gatedReadSystem{serverSystem: s.srv.sys, path: path, reads: make(chan struct{}, 2), release: make(chan struct{})}
	s.srv.sys = sys
	s.srv.configs.loadProject = func(string, *config.Config) (*config.Config, error) { return &config.Config{}, nil }
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1", DirPath: dir}, nil)
	ch := &db.Channel{ChannelID: "ch-1", DirPath: dir}

	errs := make(chan error, 2)
	apply := func(name string) {
		_, _, _, err := s.srv.editLearnConfig(context.Background(), ch, &learn.PromptShortcut{Name: name, Prompt: "p"}, true)
		errs <- err
	}
	go apply("one")
	<-sys.reads
	go apply("two")
	select {
	case <-sys.reads:
		s.FailNow("the second edit read the config while the first held it")
	case <-time.After(50 * time.Millisecond):
	}
	close(sys.release)
	require.NoError(s.T(), <-errs)
	require.NoError(s.T(), <-errs)

	data, err := os.ReadFile(path)
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(data), `"one"`)
	require.Contains(s.T(), string(data), `"two"`)
}
