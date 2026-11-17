package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/agentgate"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
)

type mockNotificationStore struct{ mock.Mock }

func (m *mockNotificationStore) InsertNotification(ctx context.Context, n *db.Notification) error {
	return m.Called(ctx, n).Error(0)
}

func (m *mockNotificationStore) ListNotifications(ctx context.Context, channelID string, limit int) ([]*db.Notification, error) {
	args := m.Called(ctx, channelID, limit)
	v, _ := args.Get(0).([]*db.Notification)
	return v, args.Error(1)
}

func (m *mockNotificationStore) UnreadNotificationCounts(ctx context.Context) (map[string]int, error) {
	args := m.Called(ctx)
	v, _ := args.Get(0).(map[string]int)
	return v, args.Error(1)
}

func (m *mockNotificationStore) MarkNotificationsRead(ctx context.Context, channelID string, upTo int64) error {
	return m.Called(ctx, channelID, upTo).Error(0)
}

func (m *mockNotificationStore) DeleteNotification(ctx context.Context, id int64) (bool, error) {
	args := m.Called(ctx, id)
	return args.Bool(0), args.Error(1)
}

func (m *mockNotificationStore) DeleteChannelNotifications(ctx context.Context, channelID string) (bool, error) {
	args := m.Called(ctx, channelID)
	return args.Bool(0), args.Error(1)
}

func (s *ServerSuite) listAttention() []attentionItem {
	s.mux.HandleFunc("GET /api/attention", s.srv.handleListAttention)
	rec := s.testRequest(http.MethodGet, "/api/attention", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	var got attentionResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &got))
	return got.Items
}

func (s *ServerSuite) TestListAttention() {
	asks := new(MockPendingAsksLister)
	asks.On("ListAskedChannels").Return([]events.AskedChannelEntry{
		{ChannelID: "t1", Data: events.AskUserQuestionEventData{ToolUseID: "tu1", Questions: []events.AskUserQuestion{
			{Question: "Which?", Options: []events.AskUserOption{{Label: "A"}, {Label: "B"}}},
			{Question: "Why?"},
		}}},
	}).Once()
	s.srv.SetPendingAsksLister(asks)
	plans := new(MockPendingPlansLister)
	plans.On("ListPlannedChannels").Return([]events.PlannedChannelEntry{
		{ChannelID: "t2", Data: events.ExitPlanModeEventData{Plan: "\n# Ship it\n\nsteps", ToolUseID: "tu2"}},
	}).Once()
	s.srv.SetPendingPlansLister(plans)
	s.srv.SetPendingApprovalLister(&fakePendingLister{entries: []agentgate.ContainerPendingApproval{
		{ContainerID: "c", PendingApproval: agentgate.PendingApproval{ReqID: "r1", ChannelID: "t2", Kind: "egress", Target: "example.com", Message: "Allow example.com?"}},
	}})
	pr := new(mockPRWatcher)
	pr.On("Flags", mock.Anything).Return(map[string]events.PRFlags{
		"t1": {Attention: events.PRAttentionFailed},
		"t2": {Attention: events.PRAttentionConflict, UnresolvedThreads: 1},
		"t3": {Attention: events.PRAttentionFixing, UnresolvedThreads: 2},
		"t4": {Attention: events.PRAttentionFixing},
		"t6": {Attention: events.PRAttentionFailed},
	}, nil).Once()
	s.srv.SetPRWatcher(pr)
	notes := new(mockNotificationStore)
	notes.On("UnreadNotificationCounts", mock.Anything).Return(map[string]int{"t1": 3, "t5": 1}, nil).Once()
	notes.On("ListNotifications", mock.Anything, "t1", 3).Return([]*db.Notification{
		{ID: 7, Kind: db.NotificationRun, Title: "Run failed", Body: "exit 1"},
		{ID: 6, Kind: db.NotificationRun, Read: true},
		{ID: 5, Kind: db.NotificationCard},
	}, nil).Once()
	notes.On("ListNotifications", mock.Anything, "t5", 1).Return(nil, errors.New("down")).Once()
	s.srv.SetNotifications(notes)
	s.store.On("GetChannel", mock.Anything, "t1").Return(&db.Channel{Name: "one", ParentID: "p1"}, nil).Once()
	s.store.On("GetChannel", mock.Anything, "p1").Return(&db.Channel{Name: "proj"}, nil).Once()
	s.store.On("GetChannel", mock.Anything, "t2").Return(nil, errors.New("down")).Once()
	s.store.On("GetChannel", mock.Anything, "t3").Return(&db.Channel{Name: "three", ParentID: "gone"}, nil).Once()
	s.store.On("GetChannel", mock.Anything, "gone").Return(nil, nil).Once()
	s.store.On("GetChannel", mock.Anything, "t6").Return(&db.Channel{Name: "six"}, nil).Once()

	require.Equal(s.T(), []attentionItem{
		{Kind: attentionAsk, ChannelID: "t1", ChannelName: "one", ParentName: "proj", Title: "Question", Detail: "Which? [A | B]\nWhy?", Ref: "tu1", Blocking: true, Questions: []events.AskUserQuestion{
			{Question: "Which?", Options: []events.AskUserOption{{Label: "A"}, {Label: "B"}}},
			{Question: "Why?"},
		}},
		{Kind: attentionGate, ChannelID: "t2", Title: "Allow example.com?", Detail: "egress example.com", Ref: "r1", Blocking: true},
		{Kind: attentionPlan, ChannelID: "t2", Title: "Plan to approve", Detail: "Ship it", Ref: "tu2", Blocking: true},
		{Kind: attentionPR, ChannelID: "t1", ChannelName: "one", ParentName: "proj", Title: "PR needs attention", Detail: "CI failed"},
		{Kind: attentionPR, ChannelID: "t2", Title: "PR needs attention", Detail: "merge conflict; 1 unresolved review thread"},
		{Kind: attentionPR, ChannelID: "t3", ChannelName: "three", Title: "PR needs attention", Detail: "2 unresolved review threads"},
		{Kind: attentionPR, ChannelID: "t6", ChannelName: "six", Title: "PR needs attention", Detail: "CI failed"},
		{Kind: attentionRun, ChannelID: "t1", ChannelName: "one", ParentName: "proj", Title: "Run failed", Detail: "exit 1", Ref: "7"},
	}, s.listAttention())
	s.store.AssertExpectations(s.T())
	notes.AssertExpectations(s.T())
}

func (s *ServerSuite) TestListAttentionEmpty() {
	tests := []struct {
		name  string
		setup func()
	}{
		{name: "nothing configured", setup: func() { s.srv.store = nil }},
		{name: "notifications unreadable", setup: func() {
			notes := new(mockNotificationStore)
			notes.On("UnreadNotificationCounts", mock.Anything).Return(nil, errors.New("down")).Once()
			s.srv.SetNotifications(notes)
		}},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			tt.setup()
			require.Empty(s.T(), s.listAttention())
		})
	}
}

func (s *ServerSuite) TestObserveAttention() {
	hub := NewEventsHub(testLogger())
	var sent []string
	hub.captureHook = func(e Event) { sent = append(sent, e.Type) }
	s.srv.SetEventsHub(hub)
	s.srv.deliverAsync = func(f func()) { f() }

	hub.Broadcast(Event{Type: EventMessageCreated, ChannelID: "t1"})
	hub.Broadcast(Event{Type: EventGateApprovalRequested, ChannelID: "t1"})
	require.Equal(s.T(), []string{EventMessageCreated, EventGateApprovalRequested, EventAttentionChanged}, sent)
}

func (s *ServerSuite) TestAttentionText() {
	require.Empty(s.T(), firstLine("\n  \n#\n"))
	require.Empty(s.T(), prAttentionText(events.PRFlags{}))
}
