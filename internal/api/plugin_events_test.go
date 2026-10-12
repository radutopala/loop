package api

import (
	"errors"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/plugin"
	"github.com/radutopala/loop/internal/plugin/proxy"
	"github.com/radutopala/loop/internal/plugin/runtime"
)

// TestPluginEventTypes holds the events a manifest may subscribe to to
// ones the hub sends.
func (s *ServerSuite) TestPluginEventTypes() {
	sent := []string{EventMessageCreated, EventAgentStatus, EventQualityScanned}
	require.ElementsMatch(s.T(), sent, plugin.Events)
}

func (s *ServerSuite) TestForwardToPlugins() {
	parent := &db.Channel{ChannelID: "ch-1", DirPath: "/src/app"}
	thread := &db.Channel{ChannelID: "th-1", ParentID: "ch-1", DirPath: "/src/app/.worktrees/th-1"}
	tests := []struct {
		name    string
		evt     Event
		setup   func(pm *MockPluginManager)
		thread  *runtime.Channel
		want    *proxy.Event
		wantErr error
	}{
		{
			name: "channel event",
			evt:  Event{Type: EventMessageCreated, ChannelID: "ch-1", Data: "hi", Timestamp: 7},
			setup: func(*MockPluginManager) {
				s.store.On("GetChannel", mock.Anything, "ch-1").Return(parent, nil).Once()
			},
			want: &proxy.Event{Type: EventMessageCreated, ChannelID: "ch-1", Data: "hi", Timestamp: 7},
		},
		{
			name: "thread event names the thread",
			evt:  Event{Type: EventAgentStatus, ChannelID: "th-1"},
			setup: func(*MockPluginManager) {
				s.store.On("GetChannel", mock.Anything, "th-1").Return(thread, nil).Once()
				s.store.On("GetChannel", mock.Anything, "ch-1").Return(parent, nil).Once()
			},
			thread: &runtime.Channel{ID: "th-1", DirPath: "/src/app/.worktrees/th-1"},
			want:   &proxy.Event{Type: EventAgentStatus, ChannelID: "ch-1", ThreadID: "th-1"},
		},
		{
			name: "delivery fails",
			evt:  Event{Type: EventQualityScanned, ChannelID: "ch-1"},
			setup: func(*MockPluginManager) {
				s.store.On("GetChannel", mock.Anything, "ch-1").Return(parent, nil).Once()
			},
			want:    &proxy.Event{Type: EventQualityScanned, ChannelID: "ch-1"},
			wantErr: errors.New("down"),
		},
		{
			name: "hidden thread",
			evt:  Event{Type: EventMessageCreated, ChannelID: "th-1"},
			setup: func(*MockPluginManager) {
				s.store.On("GetChannel", mock.Anything, "th-1").Return(&db.Channel{ChannelID: "th-1", ParentID: "ch-1", Kind: db.PluginKind("echo")}, nil).Once()
			},
		},
		{
			name: "unknown channel",
			evt:  Event{Type: EventMessageCreated, ChannelID: "ch-9"},
			setup: func(*MockPluginManager) {
				s.store.On("GetChannel", mock.Anything, "ch-9").Return(nil, nil).Once()
			},
		},
		{
			name: "parent unreadable",
			evt:  Event{Type: EventMessageCreated, ChannelID: "th-1"},
			setup: func(*MockPluginManager) {
				s.store.On("GetChannel", mock.Anything, "th-1").Return(thread, nil).Once()
				s.store.On("GetChannel", mock.Anything, "ch-1").Return(nil, errors.New("db")).Once()
			},
		},
		{name: "type no plugin may subscribe to", evt: Event{Type: EventToolUse, ChannelID: "ch-1"}},
		{name: "global event", evt: Event{Type: EventMessageCreated}},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			pm := new(MockPluginManager)
			s.srv.plugins = pm
			var ran int
			s.srv.deliverAsync = func(f func()) { ran++; f() }
			if tt.setup != nil {
				tt.setup(pm)
			}
			if tt.want != nil {
				pm.On("Deliver", mock.Anything, runtime.Channel{ID: "ch-1", DirPath: "/src/app"}, tt.thread, *tt.want).Return(tt.wantErr).Once()
			}
			s.srv.ForwardToPlugins(tt.evt)
			require.Equal(s.T(), tt.setup != nil, ran == 1)
			pm.AssertExpectations(s.T())
			s.store.AssertExpectations(s.T())
		})
	}
}

func (s *ServerSuite) TestForwardToPluginsWithoutPlugins() {
	s.srv.deliverAsync = func(func()) { s.Fail("delivered") }
	s.srv.ForwardToPlugins(Event{Type: EventMessageCreated, ChannelID: "ch-1"})
}

func (s *ServerSuite) TestForwardToPluginsInTheBackground() {
	pm := new(MockPluginManager)
	s.srv.plugins = pm
	done := make(chan struct{})
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1"}, nil).Once()
	pm.On("Deliver", mock.Anything, runtime.Channel{ID: "ch-1"}, (*runtime.Channel)(nil), proxy.Event{Type: EventMessageCreated, ChannelID: "ch-1"}).
		Run(func(mock.Arguments) { close(done) }).Return(nil).Once()
	s.srv.ForwardToPlugins(Event{Type: EventMessageCreated, ChannelID: "ch-1"})
	<-done
}
