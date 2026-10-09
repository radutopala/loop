package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/apiauth"
	"github.com/radutopala/loop/internal/uibridge"
)

type UIHandlerSuite struct {
	suite.Suite
	srv *Server
	ts  *httptest.Server
}

func TestUIHandlerSuite(t *testing.T) {
	suite.Run(t, new(UIHandlerSuite))
}

func (s *UIHandlerSuite) SetupTest() {
	s.srv = nilServer()
	s.ts = httptest.NewServer(s.srv.buildMux())
}

func (s *UIHandlerSuite) TearDownTest() {
	s.ts.Close()
}

// dial opens a window's connection, saying hello as clientID when it isn't
// "".
func (s *UIHandlerSuite) dial(clientID string) *websocket.Conn {
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(s.ts.URL, "http")+"/api/ws/ui", nil)
	require.NoError(s.T(), err)
	s.T().Cleanup(func() { conn.Close() })
	if clientID != "" {
		require.NoError(s.T(), conn.WriteJSON(uibridge.Message{Type: uibridge.MsgHello, ClientID: clientID}))
	}
	return conn
}

// waitVersion waits until the bridge's state is at least version v.
func (s *UIHandlerSuite) waitVersion(v uint64) {
	require.Eventually(s.T(), func() bool { return s.srv.ui.Snapshot().Version >= v }, 2*time.Second, 5*time.Millisecond)
}

func (s *UIHandlerSuite) post(body string) *http.Response {
	resp, err := http.Post(s.ts.URL+"/api/ui/commands", "application/json", strings.NewReader(body))
	require.NoError(s.T(), err)
	s.T().Cleanup(func() { resp.Body.Close() })
	return resp
}

func (s *UIHandlerSuite) TestWSRoundTrip() {
	conn := s.dial("w1")
	s.waitVersion(1)
	require.NoError(s.T(), conn.WriteMessage(websocket.TextMessage, []byte("not json")))
	require.NoError(s.T(), conn.WriteJSON(uibridge.Message{Type: uibridge.MsgState, State: json.RawMessage(`{"focused":true,"tab":"Chat"}`)}))
	s.waitVersion(2)

	go func() {
		var cmd uibridge.Message
		if conn.ReadJSON(&cmd) != nil {
			return
		}
		_ = conn.WriteJSON(uibridge.Message{Type: uibridge.MsgResult, ID: cmd.ID, Results: cmd.Steps})
	}()
	resp := s.post(`{"steps":[{"op":"set_tab","tab":"Git"}]}`)
	require.Equal(s.T(), http.StatusOK, resp.StatusCode)
	var reply uibridge.Reply
	require.NoError(s.T(), json.NewDecoder(resp.Body).Decode(&reply))
	require.Equal(s.T(), "w1", reply.ClientID)
	require.JSONEq(s.T(), `[{"op":"set_tab","tab":"Git"}]`, string(reply.Results))

	conn.Close()
	s.waitVersion(3)
	require.Empty(s.T(), s.srv.ui.Snapshot().Clients)
}

func (s *UIHandlerSuite) TestWSBadHello() {
	tests := []struct {
		name  string
		hello string
	}{
		{"not json", `nope`},
		{"wrong type", `{"type":"state","client_id":"w1"}`},
		{"no client id", `{"type":"hello"}`},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			conn := s.dial("")
			require.NoError(s.T(), conn.WriteMessage(websocket.TextMessage, []byte(tt.hello)))
			_, _, err := conn.ReadMessage()
			require.Error(s.T(), err, "the daemon closes the connection")
			require.Empty(s.T(), s.srv.ui.Snapshot().Clients)
		})
	}
}

func (s *UIHandlerSuite) TestWSUpgradeFails() {
	resp, err := http.Get(s.ts.URL + "/api/ws/ui")
	require.NoError(s.T(), err)
	defer resp.Body.Close()
	require.Equal(s.T(), http.StatusBadRequest, resp.StatusCode)
}

func (s *UIHandlerSuite) TestGetState() {
	conn := s.dial("w1")
	s.waitVersion(1)

	resp, err := http.Get(s.ts.URL + "/api/ui/state")
	require.NoError(s.T(), err)
	defer resp.Body.Close()
	var snap uibridge.Snapshot
	require.NoError(s.T(), json.NewDecoder(resp.Body).Decode(&snap))
	require.Equal(s.T(), uint64(1), snap.Version)
	require.Len(s.T(), snap.Clients, 1)

	got := make(chan uibridge.Snapshot)
	go func() {
		resp, err := http.Get(s.ts.URL + "/api/ui/state?after=1")
		if err != nil {
			close(got)
			return
		}
		defer resp.Body.Close()
		var snap uibridge.Snapshot
		_ = json.NewDecoder(resp.Body).Decode(&snap)
		got <- snap
	}()
	time.Sleep(20 * time.Millisecond)
	require.NoError(s.T(), conn.WriteJSON(uibridge.Message{Type: uibridge.MsgState, State: json.RawMessage(`{"tab":"Git"}`)}))
	snap = <-got
	require.Equal(s.T(), uint64(2), snap.Version)
	require.JSONEq(s.T(), `{"tab":"Git"}`, string(snap.Clients[0].State))
}

func (s *UIHandlerSuite) TestGetStateWaitTimesOut() {
	s.srv.uiStateWaitOverride = 10 * time.Millisecond
	resp, err := http.Get(s.ts.URL + "/api/ui/state?after=0")
	require.NoError(s.T(), err)
	defer resp.Body.Close()
	var snap uibridge.Snapshot
	require.NoError(s.T(), json.NewDecoder(resp.Body).Decode(&snap))
	require.Equal(s.T(), uint64(0), snap.Version)
}

func (s *UIHandlerSuite) TestGetStateBadAfter() {
	resp, err := http.Get(s.ts.URL + "/api/ui/state?after=x")
	require.NoError(s.T(), err)
	defer resp.Body.Close()
	require.Equal(s.T(), http.StatusBadRequest, resp.StatusCode)
}

func (s *UIHandlerSuite) TestRunCommandErrors() {
	tests := []struct {
		name string
		body string
		code int
	}{
		{"bad body", `nope`, http.StatusBadRequest},
		{"no steps", `{"steps":[]}`, http.StatusBadRequest},
		{"steps not a list", `{"steps":{"op":"x"}}`, http.StatusBadRequest},
		{"step without op", `{"steps":[{"tab":"Chat"}]}`, http.StatusBadRequest},
		{"bad timeout", `{"steps":[{"op":"x"}],"timeout":"soon"}`, http.StatusBadRequest},
		{"timeout too long", `{"steps":[{"op":"x"}],"timeout":"11m"}`, http.StatusBadRequest},
		{"no window", `{"steps":[{"op":"x"}],"timeout":"1s"}`, http.StatusNotFound},
		{"unknown window", `{"client_id":"w9","steps":[{"op":"x"}]}`, http.StatusNotFound},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			require.Equal(s.T(), tt.code, s.post(tt.body).StatusCode)
		})
	}
}

func (s *UIHandlerSuite) TestRunCommandTimesOut() {
	s.dial("w1")
	s.waitVersion(1)
	require.Equal(s.T(), http.StatusGatewayTimeout, s.post(`{"steps":[{"op":"x"}],"timeout":"20ms"}`).StatusCode)
}

func (s *UIHandlerSuite) TestRunCommandWindowDisconnects() {
	conn := s.dial("w1")
	s.waitVersion(1)
	go func() {
		_, _, _ = conn.ReadMessage()
		conn.Close()
	}()
	require.Equal(s.T(), http.StatusBadGateway, s.post(`{"steps":[{"op":"x"}]}`).StatusCode)
}

func (s *UIHandlerSuite) TestTerminalStepsOnlyOnAgentPanes() {
	conn := s.dial("w1")
	s.waitVersion(1)
	state := `{"panes":[{"id":"chat","panel":"chat"},{"id":"host-shell-0","panel":"host-shell"},{"id":"docker-agent-1","panel":"docker-agent"},{"id":"docker-shell-2","panel":"docker-shell"}]}`
	require.NoError(s.T(), conn.WriteJSON(uibridge.Message{Type: uibridge.MsgState, State: json.RawMessage(state)}))
	s.waitVersion(2)

	ops := []string{"send_input", "read_output", "wait_for"}
	for _, op := range ops {
		for _, pane := range []string{"host-shell", "host-shell-0", "chat", "missing", ""} {
			s.Run(op+" refuses "+pane, func() {
				resp := s.post(`{"steps":[{"op":"select_channel","channel_id":"c1"},{"op":"` + op + `","pane":"` + pane + `","text":"ls"}]}`)
				require.Equal(s.T(), http.StatusBadRequest, resp.StatusCode)
				body, _ := io.ReadAll(resp.Body)
				require.Contains(s.T(), string(body), op+" only works on docker-agent and docker-shell panes")
			})
		}
	}

	go func() {
		for {
			var cmd uibridge.Message
			if conn.ReadJSON(&cmd) != nil {
				return
			}
			_ = conn.WriteJSON(uibridge.Message{Type: uibridge.MsgResult, ID: cmd.ID, Results: json.RawMessage(`[]`)})
		}
	}()
	for _, op := range ops {
		for _, pane := range []string{"docker-agent", "docker-shell", "docker-agent-1", "docker-shell-2"} {
			s.Run(op+" allows "+pane, func() {
				resp := s.post(`{"steps":[{"op":"` + op + `","pane":"` + pane + `","text":"hi"}]}`)
				require.Equal(s.T(), http.StatusOK, resp.StatusCode)
			})
		}
	}
}

// asAgent serves the daemon's routes as the agent of channel ch-1.
func (s *UIHandlerSuite) asAgent() *httptest.Server {
	mux := s.srv.buildMux()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r.WithContext(apiauth.WithPrincipal(r.Context(), apiauth.Principal{Kind: apiauth.KindAgent, ChannelID: "ch-1"})))
	}))
	s.T().Cleanup(ts.Close)
	return ts
}

func (s *UIHandlerSuite) TestAgentCommandsStayInItsProject() {
	ts := s.asAgent()
	tests := []struct {
		name  string
		steps string
		code  int
		want  string
	}{
		{"not opening a channel first", `[{"op":"set_tab","tab":"Git"}]`, http.StatusForbidden, "starts with select_channel"},
		{"another project's channel", `[{"op":"select_channel","channel_id":"other"}]`, http.StatusForbidden, "channel other is outside this agent's project"},
		{"another project's channel later", `[{"op":"select_channel","channel_id":"ch-1"},{"op":"select_channel","channel_id":"other"}]`, http.StatusForbidden, "channel other is outside"},
		// Past the check, to no window.
		{"its own channel", `[{"op":"select_channel","channel_id":"ch-1"},{"op":"set_tab","tab":"Git"}]`, http.StatusNotFound, "no app window"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			resp, err := http.Post(ts.URL+"/api/ui/commands", "application/json", strings.NewReader(`{"steps":`+tt.steps+`}`))
			require.NoError(s.T(), err)
			defer resp.Body.Close()
			body, _ := io.ReadAll(resp.Body)
			require.Equal(s.T(), tt.code, resp.StatusCode, string(body))
			require.Contains(s.T(), string(body), tt.want)
		})
	}
}

func (s *UIHandlerSuite) TestAgentSeesOnlyItsProjectsWindows() {
	for i, state := range []string{`{"channel_id":"ch-1","tab":"Chat"}`, `{"channel_id":"other","tab":"Git"}`, ``} {
		conn := s.dial([]string{"w1", "w2", "w3"}[i])
		if state != "" {
			require.NoError(s.T(), conn.WriteJSON(uibridge.Message{Type: uibridge.MsgState, State: json.RawMessage(state)}))
		}
	}
	s.waitVersion(5)
	ts := s.asAgent()
	for _, query := range []string{"", "?after=0"} {
		resp, err := http.Get(ts.URL + "/api/ui/state" + query)
		require.NoError(s.T(), err)
		var snap uibridge.Snapshot
		require.NoError(s.T(), json.NewDecoder(resp.Body).Decode(&snap))
		resp.Body.Close()
		states := map[string]string{}
		for _, c := range snap.Clients {
			states[c.ClientID] = string(c.State)
		}
		require.Equal(s.T(), map[string]string{"w1": `{"channel_id":"ch-1","tab":"Chat"}`, "w2": "", "w3": ""}, states)
	}
}
