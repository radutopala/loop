package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"

	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/config"
)

// uiServer points the app at a daemon that answers with handler.
func (s *MainSuite) uiServer(handler http.HandlerFunc) {
	srv := httptest.NewServer(handler)
	s.T().Cleanup(srv.Close)
	s.app.configLoad = func() (*config.Config, error) {
		return &config.Config{APIAddr: srv.Listener.Addr().String()}, nil
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

type failReader struct{}

func (failReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

// runUI runs `ui <args>` with stdin and returns its output.
func (s *MainSuite) runUI(stdin io.Reader, args ...string) (string, error) {
	cmd := s.app.newUICmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetIn(stdin)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

func (s *MainSuite) TestNewUICmd() {
	cmd := s.app.newUICmd()
	require.Equal(s.T(), "ui", cmd.Use)
	var names []string
	for _, c := range cmd.Commands() {
		names = append(names, c.Name())
	}
	require.Equal(s.T(), []string{"run", "state"}, names)
}

func (s *MainSuite) TestUIRun() {
	tests := []struct {
		name    string
		args    []string
		stdin   string
		status  int
		reply   string
		wantErr string
		wantOut string
	}{
		{
			name:    "steps from the argument",
			args:    []string{`[{"op":"set_tab","tab":"Git"}]`, "--client", "w1", "--timeout", "5s"},
			status:  http.StatusOK,
			reply:   `{"client_id":"w1","results":[{"ok":true}]}`,
			wantOut: "{\n  \"client_id\": \"w1\",\n  \"results\": [\n    {\n      \"ok\": true\n    }\n  ]\n}\n",
		},
		{
			name:    "steps from stdin",
			args:    []string{"-"},
			stdin:   `[{"op":"set_tab","tab":"Git"}]`,
			status:  http.StatusOK,
			reply:   `{"client_id":"","results":[{"ok":true}]}`,
			wantOut: "\"ok\": true",
		},
		{
			name:    "a step fails",
			args:    []string{`[{"op":"set_tab","tab":"Git"}]`},
			status:  http.StatusOK,
			reply:   `{"results":[{"ok":true},{"ok":false,"error":"no such tab"}]}`,
			wantErr: "step 2 failed",
			wantOut: "no such tab",
		},
		{
			name:    "the command fails",
			args:    []string{`[{"op":"set_tab","tab":"Git"}]`},
			status:  http.StatusOK,
			reply:   `{"error":"unknown op"}`,
			wantErr: "unknown op",
		},
		{
			name:    "the daemon refuses",
			args:    []string{`[{"op":"send_input","pane":"host-shell"}]`},
			status:  http.StatusBadRequest,
			reply:   "refused\n",
			wantErr: "400 Bad Request: refused",
		},
		{
			name:    "the answer isn't JSON",
			args:    []string{`[]`},
			status:  http.StatusOK,
			reply:   `nope`,
			wantErr: "parsing the results",
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			var got atomic.Value
			s.uiServer(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(s.T(), http.MethodPost, r.Method)
				require.Equal(s.T(), "/api/ui/commands", r.URL.Path)
				require.Equal(s.T(), "application/json", r.Header.Get("Content-Type"))
				body, _ := io.ReadAll(r.Body)
				got.Store(string(body))
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.reply)
			})
			out, err := s.runUI(strings.NewReader(tt.stdin), append([]string{"run"}, tt.args...)...)
			if tt.wantErr != "" {
				require.ErrorContains(s.T(), err, tt.wantErr)
			} else {
				require.NoError(s.T(), err)
			}
			require.Contains(s.T(), out, tt.wantOut)
			require.Contains(s.T(), got.Load(), `"steps":[`)
		})
	}
}

func (s *MainSuite) TestUIRunSendsClientAndTimeout() {
	var got string
	s.uiServer(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = string(body)
		_, _ = io.WriteString(w, `{"results":[]}`)
	})
	_, err := s.runUI(nil, "run", `[{"op":"x"}]`, "--client", "w1", "--timeout", "5s")
	require.NoError(s.T(), err)
	require.JSONEq(s.T(), `{"client_id":"w1","steps":[{"op":"x"}],"timeout":"5s"}`, got)
}

func (s *MainSuite) TestUIRunBadInput() {
	_, err := s.runUI(nil, "run", `[{"op"`)
	require.EqualError(s.T(), err, "the steps aren't valid JSON")

	_, err = s.runUI(failReader{})
	require.NoError(s.T(), err, "the parent alone just prints its help")

	_, err = s.runUI(failReader{}, "run")
	require.EqualError(s.T(), err, "reading the steps: read failed")
}

func (s *MainSuite) TestUIRunWriteFails() {
	s.uiServer(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"results":[]}`)
	})
	err := s.app.runUISteps(context.Background(), failWriter{}, "", "", []byte(`[]`))
	require.EqualError(s.T(), err, "write failed")
}

func (s *MainSuite) TestUIRequestErrors() {
	s.Run("bad address", func() {
		s.app.configLoad = func() (*config.Config, error) { return &config.Config{APIAddr: "bad host"}, nil }
		_, err := s.app.uiRequest(context.Background(), http.MethodGet, "/api/ui/state", nil)
		require.ErrorContains(s.T(), err, "building the request")
	})
	s.Run("daemon down", func() {
		s.app.configLoad = func() (*config.Config, error) { return &config.Config{APIAddr: "127.0.0.1:1"}, nil }
		_, err := s.app.uiRequest(context.Background(), http.MethodGet, "/api/ui/state", nil)
		require.ErrorContains(s.T(), err, "calling the UI API")
	})
	s.Run("body cut short", func() {
		s.uiServer(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "100")
			_, _ = io.WriteString(w, `{"ver`)
		})
		_, err := s.app.uiRequest(context.Background(), http.MethodGet, "/api/ui/state", nil)
		require.ErrorContains(s.T(), err, "reading the answer")
	})
}

func (s *MainSuite) TestUIState() {
	s.uiServer(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(s.T(), "/api/ui/state", r.URL.Path)
		require.Empty(s.T(), r.URL.RawQuery)
		_, _ = io.WriteString(w, `{"version":3,"clients":[]}`)
	})
	out, err := s.runUI(nil, "state")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "{\n  \"version\": 3,\n  \"clients\": []\n}\n", out)
}

func (s *MainSuite) TestUIStateErrors() {
	s.app.configLoad = func() (*config.Config, error) { return &config.Config{APIAddr: "127.0.0.1:1"}, nil }
	_, err := s.runUI(nil, "state")
	require.ErrorContains(s.T(), err, "calling the UI API")

	s.uiServer(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `nope`)
	})
	_, err = s.runUI(nil, "state")
	require.ErrorContains(s.T(), err, "formatting the answer")
}

func (s *MainSuite) TestUIStateWatch() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	answers := []string{
		`{"version":1,"clients":[]}`,
		`{"version":1,"clients":[]}`, // the wait ran out
		`{"version": 2, "clients": [{"client_id":"w1"}]}`,
	}
	var queries []string
	s.uiServer(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		if len(queries) > len(answers) {
			cancel()
			<-r.Context().Done()
			return
		}
		_, _ = io.WriteString(w, answers[len(queries)-1])
	})
	cmd := s.app.newUICmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"state", "--watch"})
	require.NoError(s.T(), cmd.ExecuteContext(ctx))
	require.Equal(s.T(), `{"version":1,"clients":[]}`+"\n"+`{"version":2,"clients":[{"client_id":"w1"}]}`+"\n", out.String())
	require.Equal(s.T(), []string{"", "after=1", "after=1", "after=2"}, queries)
}

func (s *MainSuite) TestUIStateWatchErrors() {
	s.Run("daemon refuses", func() {
		s.uiServer(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "nope", http.StatusUnauthorized)
		})
		err := s.app.watchUIState(context.Background(), io.Discard)
		require.ErrorContains(s.T(), err, "401 Unauthorized: nope")
	})
	s.Run("answer isn't JSON", func() {
		s.uiServer(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `nope`)
		})
		err := s.app.watchUIState(context.Background(), io.Discard)
		require.ErrorContains(s.T(), err, "parsing the state")
	})
	s.Run("write fails", func() {
		s.uiServer(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"version":1}`)
		})
		err := s.app.watchUIState(context.Background(), failWriter{})
		require.EqualError(s.T(), err, "write failed")
	})
}
