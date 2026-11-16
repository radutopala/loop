package api

import (
	"cmp"
	"context"
	"errors"
	"net/http"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/promptedit"
)

type MockPromptEditor struct {
	mock.Mock
}

func (m *MockPromptEditor) PromptEdit(ctx context.Context, ch *db.Channel, req promptedit.Request) (*promptedit.Result, error) {
	args := m.Called(ctx, ch, req)
	r, _ := args.Get(0).(*promptedit.Result)
	return r, args.Error(1)
}

func (s *ServerSuite) TestPromptEditNotConfigured() {
	w := s.explainRequest("POST", "ch-1/prompt-edit", `{}`)
	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

func (s *ServerSuite) TestPromptEdit() {
	ch := &db.Channel{ChannelID: "ch-1", DirPath: "/work", ModelOverride: "opus", EffortOverride: "low"}
	want := promptedit.Request{Draft: "fix it", Start: 1, End: 3, Instruction: "shorter"}
	body := `{"draft":"fix it","start":1,"end":3,"instruction":"shorter"}`
	tests := []struct {
		name     string
		body     string
		channel  string
		result   *promptedit.Result
		err      error
		wantCode int
		wantBody string
	}{
		{name: "bad body", body: `{`, wantCode: http.StatusBadRequest},
		{name: "channel gone", body: body, channel: "gone", wantCode: http.StatusNotFound},
		{name: "edits", body: body, result: &promptedit.Result{Text: "fx it", From: 1, To: 2}, wantCode: http.StatusOK, wantBody: `{"text":"fx it","from":1,"to":2}`},
		{name: "no instruction", body: body, err: promptedit.ErrNoInstruction, wantCode: http.StatusBadRequest},
		{name: "run fails", body: body, err: errors.New("boom"), wantCode: http.StatusInternalServerError, wantBody: "boom"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			editor := new(MockPromptEditor)
			s.srv.SetPromptEditor(editor)
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(ch, nil).Maybe()
			s.store.On("GetChannel", mock.Anything, "gone").Return(nil, nil).Maybe()
			editor.On("PromptEdit", mock.Anything, ch, want).Return(tc.result, tc.err).Maybe()

			w := s.explainRequest("POST", cmp.Or(tc.channel, "ch-1")+"/prompt-edit", tc.body)
			require.Equal(s.T(), tc.wantCode, w.Code, w.Body.String())
			if tc.wantBody != "" {
				require.Contains(s.T(), w.Body.String(), tc.wantBody)
			}
			if tc.result != nil || tc.err != nil {
				editor.AssertExpectations(s.T())
			}
		})
	}
}

func (s *ServerSuite) TestPromptEditState() {
	ch := &db.Channel{ChannelID: "ch-1"}
	tests := []struct {
		name     string
		channel  string
		thread   *db.Channel
		err      error
		wantCode int
		wantBody string
	}{
		{name: "channel gone", channel: "gone", wantCode: http.StatusNotFound},
		{name: "no edits yet", wantCode: http.StatusOK, wantBody: `{"edit_channel_id":""}`},
		{name: "thread", thread: &db.Channel{ChannelID: "prompt-edit-abc"}, wantCode: http.StatusOK, wantBody: `{"edit_channel_id":"prompt-edit-abc"}`},
		{name: "lookup fails", err: errors.New("boom"), wantCode: http.StatusInternalServerError, wantBody: "boom"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(ch, nil).Maybe()
			s.store.On("GetChannel", mock.Anything, "gone").Return(nil, nil).Maybe()
			s.store.On("GetHiddenThread", mock.Anything, "ch-1", db.ChannelKindPromptEdit).Return(tc.thread, tc.err).Maybe()

			w := s.explainRequest("GET", cmp.Or(tc.channel, "ch-1")+"/prompt-edit", "")
			require.Equal(s.T(), tc.wantCode, w.Code, w.Body.String())
			require.Contains(s.T(), w.Body.String(), tc.wantBody)
		})
	}
}
