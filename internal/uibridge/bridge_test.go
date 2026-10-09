package uibridge

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type BridgeSuite struct {
	suite.Suite
	b   *Bridge
	now time.Time
}

func TestBridgeSuite(t *testing.T) {
	suite.Run(t, new(BridgeSuite))
}

func (s *BridgeSuite) SetupTest() {
	s.now = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	s.b = newBridge(func() time.Time { return s.now })
}

// tick moves the bridge's clock on, so each event gets its own time.
func (s *BridgeSuite) tick() {
	s.now = s.now.Add(time.Second)
}

// window is a fake app window: it records what it's sent.
type window struct {
	mu   sync.Mutex
	sent []Message
	got  chan Message
	err  error
}

func newWindow() *window {
	return &window{got: make(chan Message, 10)}
}

func (w *window) send(data []byte) error {
	if w.err != nil {
		return w.err
	}
	var m Message
	if err := json.Unmarshal(data, &m); err != nil {
		return err
	}
	w.mu.Lock()
	w.sent = append(w.sent, m)
	w.mu.Unlock()
	w.got <- m
	return nil
}

func (s *BridgeSuite) state(clientID, raw string) {
	s.b.Handle(clientID, Message{Type: MsgState, State: json.RawMessage(raw)})
}

func noCheck(Client) error { return nil }

func (s *BridgeSuite) ids() []string {
	var ids []string
	for _, c := range s.b.Snapshot().Clients {
		ids = append(ids, c.ClientID)
	}
	return ids
}

func (s *BridgeSuite) TestNew() {
	require.Empty(s.T(), New().Snapshot().Clients)
}

func (s *BridgeSuite) TestAttachAndDetach() {
	detach := s.b.Attach("w1", newWindow().send)
	snap := s.b.Snapshot()
	require.Equal(s.T(), uint64(1), snap.Version)
	require.Equal(s.T(), []Client{{ClientID: "w1", ConnectedAt: s.now}}, snap.Clients)

	detach()
	snap = s.b.Snapshot()
	require.Equal(s.T(), uint64(2), snap.Version)
	require.Empty(s.T(), snap.Clients)

	detach() // a second detach changes nothing
	require.Equal(s.T(), uint64(2), s.b.Snapshot().Version)
}

func (s *BridgeSuite) TestReattachKeepsStateAndOldDetachIsNoop() {
	oldDetach := s.b.Attach("w1", newWindow().send)
	s.tick()
	s.state("w1", `{"focused":true,"tab":"Chat"}`)
	focusedAt := s.now

	s.tick()
	s.b.Attach("w1", newWindow().send)
	oldDetach()

	snap := s.b.Snapshot()
	require.Len(s.T(), snap.Clients, 1)
	c := snap.Clients[0]
	require.True(s.T(), c.Focused)
	require.Equal(s.T(), focusedAt, c.FocusedAt)
	require.Equal(s.T(), s.now, c.ConnectedAt)
	require.JSONEq(s.T(), `{"focused":true,"tab":"Chat"}`, string(c.State))
}

func (s *BridgeSuite) TestStateTracksFocus() {
	s.b.Attach("w1", newWindow().send)
	s.tick()
	s.state("w1", `{"focused":true}`)
	first := s.now

	s.tick()
	s.state("w1", `{"focused":true,"tab":"Git"}`)
	c := s.b.Snapshot().Clients[0]
	require.Equal(s.T(), first, c.FocusedAt, "staying focused keeps when focus came")

	s.tick()
	s.state("w1", `not json`)
	c = s.b.Snapshot().Clients[0]
	require.False(s.T(), c.Focused)
	require.Equal(s.T(), first, c.FocusedAt)
}

func (s *BridgeSuite) TestStateFromUnknownClientIgnored() {
	s.state("ghost", `{"focused":true}`)
	require.Equal(s.T(), uint64(0), s.b.Snapshot().Version)
}

func (s *BridgeSuite) TestOrder() {
	tests := []struct {
		name  string
		setup func()
		want  []string
	}{
		{
			name: "focused first",
			setup: func() {
				s.state("a", `{"focused":true}`)
				s.tick()
				s.state("b", `{"focused":true}`)
				s.tick()
				s.state("b", `{"focused":false}`)
			},
			want: []string{"a", "b", "c"},
		},
		{
			name: "then focused last",
			setup: func() {
				s.state("a", `{"focused":true}`)
				s.tick()
				s.state("a", `{"focused":false}`)
				s.tick()
				s.state("b", `{"focused":true}`)
				s.tick()
				s.state("b", `{"focused":false}`)
			},
			want: []string{"b", "a", "c"},
		},
		{
			name:  "then connected last",
			setup: func() {},
			want:  []string{"c", "b", "a"},
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			for _, id := range []string{"a", "b", "c"} {
				s.tick()
				s.b.Attach(id, newWindow().send)
			}
			tt.setup()
			require.Equal(s.T(), tt.want, s.ids())
		})
	}
}

func (s *BridgeSuite) TestOrderTieBreaksOnID() {
	s.b.Attach("b", newWindow().send)
	s.b.Attach("a", newWindow().send)
	require.Equal(s.T(), []string{"a", "b"}, s.ids())
}

func (s *BridgeSuite) TestRunRoundTrip() {
	w1, w2 := newWindow(), newWindow()
	s.b.Attach("w1", w1.send)
	s.tick()
	s.b.Attach("w2", w2.send)
	s.state("w1", `{"focused":true}`)

	var checked Client
	done := make(chan struct{})
	var reply Reply
	var err error
	go func() {
		defer close(done)
		reply, err = s.b.Run(context.Background(), "", json.RawMessage(`[{"op":"set_tab"}]`), func(c Client) error {
			checked = c
			return nil
		})
	}()
	cmd := <-w1.got
	require.Equal(s.T(), MsgCommand, cmd.Type)
	require.Equal(s.T(), "cmd-1", cmd.ID)
	require.JSONEq(s.T(), `[{"op":"set_tab"}]`, string(cmd.Steps))
	require.Zero(s.T(), cmd.TimeoutMS, "a command with no deadline has no timeout")

	s.b.Handle("w2", Message{Type: MsgResult, ID: cmd.ID})  // another window can't answer it
	s.b.Handle("w1", Message{Type: MsgResult, ID: "cmd-9"}) // nor can an unknown id
	s.b.Handle("w1", Message{Type: MsgResult, ID: cmd.ID, Results: json.RawMessage(`[{"ok":true}]`), Error: "e"})
	<-done

	require.NoError(s.T(), err)
	require.Equal(s.T(), "w1", checked.ClientID)
	require.Equal(s.T(), "w1", reply.ClientID)
	require.JSONEq(s.T(), `[{"ok":true}]`, string(reply.Results))
	require.Equal(s.T(), "e", reply.Error)
	require.Empty(s.T(), w2.sent)
	require.Empty(s.T(), s.b.pending)
}

func (s *BridgeSuite) TestRunByClientID() {
	w1, w2 := newWindow(), newWindow()
	s.b.Attach("w1", w1.send)
	s.b.Attach("w2", w2.send)
	timeout := make(chan int64, 1)
	go func() {
		cmd := <-w2.got
		timeout <- cmd.TimeoutMS
		s.b.Handle("w2", Message{Type: MsgResult, ID: cmd.ID, Results: json.RawMessage(`[]`)})
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	reply, err := s.b.Run(ctx, "w2", json.RawMessage(`[]`), noCheck)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "w2", reply.ClientID)
	require.InDelta(s.T(), time.Minute.Milliseconds(), <-timeout, 5000, "the window gets the time left")
}

func (s *BridgeSuite) TestRunErrors() {
	_, err := s.b.Run(context.Background(), "", nil, noCheck)
	require.ErrorIs(s.T(), err, ErrNoClient)

	s.b.Attach("w1", newWindow().send)
	_, err = s.b.Run(context.Background(), "nope", nil, noCheck)
	require.ErrorIs(s.T(), err, ErrUnknownClient)

	refused := errors.New("refused")
	_, err = s.b.Run(context.Background(), "", nil, func(Client) error { return refused })
	require.ErrorIs(s.T(), err, refused)
	require.Empty(s.T(), s.b.pending)
}

func (s *BridgeSuite) TestRunSendFails() {
	w := newWindow()
	w.err = errors.New("closed")
	s.b.Attach("w1", w.send)
	_, err := s.b.Run(context.Background(), "", nil, noCheck)
	require.EqualError(s.T(), err, "sending the command: closed")
	require.Empty(s.T(), s.b.pending)
}

func (s *BridgeSuite) TestRunContextDone() {
	w := newWindow()
	s.b.Attach("w1", w.send)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-w.got
		cancel()
	}()
	_, err := s.b.Run(ctx, "", nil, noCheck)
	require.ErrorIs(s.T(), err, context.Canceled)
	require.Empty(s.T(), s.b.pending)
}

func (s *BridgeSuite) TestRunWindowDisconnects() {
	w := newWindow()
	detach := s.b.Attach("w1", w.send)
	other := newWindow()
	s.b.Attach("w2", other.send)
	go func() {
		<-w.got
		detach()
	}()
	_, err := s.b.Run(context.Background(), "w1", nil, noCheck)
	require.ErrorIs(s.T(), err, ErrDisconnected)
}

func (s *BridgeSuite) TestWait() {
	s.b.Attach("w1", newWindow().send)

	snap := s.b.Wait(context.Background(), 0)
	require.Equal(s.T(), uint64(1), snap.Version, "a newer version answers at once")

	got := make(chan Snapshot)
	go func() { got <- s.b.Wait(context.Background(), 1) }()
	time.Sleep(10 * time.Millisecond)
	s.state("w1", `{"tab":"Chat"}`)
	snap = <-got
	require.Equal(s.T(), uint64(2), snap.Version)
	require.JSONEq(s.T(), `{"tab":"Chat"}`, string(snap.Clients[0].State))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	snap = s.b.Wait(ctx, 2)
	require.Equal(s.T(), uint64(2), snap.Version, "a done context answers with the state as it is")
}
