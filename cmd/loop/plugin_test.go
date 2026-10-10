package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"

	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/config"
)

// pluginCall is a request the fake daemon got.
type pluginCall struct {
	method, path, body string
}

// pluginReply is the fake daemon's answer to a request.
type pluginReply struct {
	status int
	body   string
}

// pluginServer serves replies keyed by "METHOD path" and records the calls.
func (s *MainSuite) pluginServer(replies map[string]pluginReply) *[]pluginCall {
	var mu sync.Mutex
	calls := &[]pluginCall{}
	s.uiServer(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		*calls = append(*calls, pluginCall{r.Method, r.URL.RequestURI(), string(body)})
		mu.Unlock()
		reply, ok := replies[r.Method+" "+r.URL.RequestURI()]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(reply.status)
		_, _ = io.WriteString(w, reply.body)
	})
	return calls
}

// runPlugin runs `plugin <args>` with stdin and returns its output.
func (s *MainSuite) runPlugin(stdin io.Reader, args ...string) (string, error) {
	cmd := s.app.newPluginCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetIn(stdin)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

const (
	echoInstalled = `{"plugins":[{"id":"echo","image":"ghcr.io/acme/echo@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","version":"1.0.0","dev":false,"status":"installed"}]}`
	echoFresh     = `{"id":"echo","name":"Echo","version":"1.0.0","image":"ghcr.io/acme/echo@sha256:aa","digest":"sha256:aa",
		"capabilities":["ui.panel","files.read"],"new_capabilities":["ui.panel","files.read"]}`
	echoUpgrade = `{"id":"echo","name":"Echo","version":"1.1.0","image":"ghcr.io/acme/echo@sha256:bb","digest":"sha256:bb",
		"capabilities":["ui.panel","files.read"],"new_capabilities":["files.read"],"previous_version":"1.0.0"}`
	echoSameCaps = `{"id":"echo","name":"Echo","version":"1.1.0","image":"ghcr.io/acme/echo@sha256:bb","digest":"sha256:bb",
		"capabilities":[],"new_capabilities":[],"previous_version":"1.0.0"}`
)

func (s *MainSuite) TestNewPluginCmd() {
	cmd := s.app.newPluginCmd()
	require.Equal(s.T(), "plugin", cmd.Use)
	var names []string
	for _, c := range cmd.Commands() {
		names = append(names, c.Name())
	}
	require.Equal(s.T(), []string{"dev", "install", "list", "remove", "upgrade"}, names)
}

func (s *MainSuite) TestPluginList() {
	tests := []struct {
		name    string
		reply   pluginReply
		wantOut string
		wantErr string
	}{
		{
			name:  "installed",
			reply: pluginReply{http.StatusOK, `{"plugins":[{"id":"echo","image":"ghcr.io/acme/echo@sha256:aa","version":"1.0.0","status":"installed"},{"id":"cogtest","image":"sha256:cc","version":"0.1.0","dev":true,"status":"installed"}]}`},
			wantOut: "ID       VERSION  STATUS     IMAGE\n" +
				"echo     1.0.0    installed  ghcr.io/acme/echo@sha256:aa\n" +
				"cogtest  0.1.0    installed  sha256:cc (dev)\n",
		},
		{name: "none", reply: pluginReply{http.StatusOK, `{"plugins":[]}`}, wantOut: "No plugins installed\n"},
		{name: "disabled", reply: pluginReply{http.StatusNotImplemented, "plugins are disabled\n"}, wantErr: "501 Not Implemented: plugins are disabled"},
		{name: "not JSON", reply: pluginReply{http.StatusOK, "nope"}, wantErr: "parsing the answer"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			s.pluginServer(map[string]pluginReply{"GET /api/plugins": tt.reply})
			out, err := s.runPlugin(nil, "list")
			if tt.wantErr != "" {
				require.ErrorContains(s.T(), err, tt.wantErr)
				return
			}
			require.NoError(s.T(), err)
			require.Equal(s.T(), tt.wantOut, out)
		})
	}
}

func (s *MainSuite) TestPluginInstall() {
	tests := []struct {
		name      string
		args      []string
		stdin     string
		inspect   pluginReply
		install   pluginReply
		wantOut   []string
		wantErr   string
		wantCalls []pluginCall
	}{
		{
			name:    "approved",
			args:    []string{"install", "ghcr.io/acme/echo:1.0.0"},
			stdin:   "y\n",
			inspect: pluginReply{http.StatusOK, echoFresh},
			install: pluginReply{http.StatusOK, echoFresh},
			wantOut: []string{"Echo 1.0.0 (echo)\nImage: ghcr.io/acme/echo@sha256:aa\nCapabilities:\n  ui.panel\n  files.read\n",
				"Its containers can reach the network.\nInstall? [y/N] Installed echo 1.0.0\n"},
			wantCalls: []pluginCall{
				{"POST", "/api/plugins/inspect", `{"dev":false,"image":"ghcr.io/acme/echo:1.0.0"}`},
				{"POST", "/api/plugins", `{"approve":["ui.panel","files.read"],"digest":"sha256:aa","dev":false,"image":"ghcr.io/acme/echo:1.0.0"}`},
			},
		},
		{
			name:    "dev with yes",
			args:    []string{"install", "echo:dev", "--dev", "--yes"},
			inspect: pluginReply{http.StatusOK, echoFresh},
			install: pluginReply{http.StatusOK, echoFresh},
			wantOut: []string{"Installed echo 1.0.0\n"},
			wantCalls: []pluginCall{
				{"POST", "/api/plugins/inspect", `{"dev":true,"image":"echo:dev"}`},
				{"POST", "/api/plugins", `{"approve":["ui.panel","files.read"],"digest":"sha256:aa","dev":true,"image":"echo:dev"}`},
			},
		},
		{
			name:      "declined",
			args:      []string{"install", "echo:dev"},
			stdin:     "n\n",
			inspect:   pluginReply{http.StatusOK, echoFresh},
			wantErr:   "not installed",
			wantCalls: []pluginCall{{"POST", "/api/plugins/inspect", `{"dev":false,"image":"echo:dev"}`}},
		},
		{
			name:    "EOF declines",
			args:    []string{"install", "echo:dev"},
			inspect: pluginReply{http.StatusOK, echoFresh},
			wantErr: "not installed",
		},
		{
			name:    "upgrade marks new capabilities",
			args:    []string{"install", "ghcr.io/acme/echo:1.1.0"},
			stdin:   "yes\n",
			inspect: pluginReply{http.StatusOK, echoUpgrade},
			install: pluginReply{http.StatusOK, echoUpgrade},
			wantOut: []string{"Upgrades 1.0.0\nCapabilities:\n  ui.panel\n  files.read (new)\n", "Installed echo 1.1.0\n"},
		},
		{
			name:    "upgrade asking nothing new",
			args:    []string{"install", "ghcr.io/acme/echo:1.1.0"},
			inspect: pluginReply{http.StatusOK, echoSameCaps},
			install: pluginReply{http.StatusOK, echoSameCaps},
			wantOut: []string{"Capabilities: none\n", "Installed echo 1.1.0\n"},
		},
		{
			name:    "inspect fails",
			args:    []string{"install", "echo:dev"},
			inspect: pluginReply{http.StatusUnprocessableEntity, "not a Loop plugin\n"},
			wantErr: "422 Unprocessable Entity: not a Loop plugin",
		},
		{
			name:    "install fails",
			args:    []string{"install", "echo:dev", "-y"},
			inspect: pluginReply{http.StatusOK, echoFresh},
			install: pluginReply{http.StatusConflict, "the image changed\n"},
			wantErr: "409 Conflict: the image changed",
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			calls := s.pluginServer(map[string]pluginReply{"POST /api/plugins/inspect": tt.inspect, "POST /api/plugins": tt.install})
			out, err := s.runPlugin(strings.NewReader(tt.stdin), tt.args...)
			if tt.wantErr != "" {
				require.EqualError(s.T(), err, tt.wantErr)
			} else {
				require.NoError(s.T(), err)
			}
			for _, want := range tt.wantOut {
				require.Contains(s.T(), out, want)
			}
			if tt.wantCalls != nil {
				require.Len(s.T(), *calls, len(tt.wantCalls))
				for i, want := range tt.wantCalls {
					got := (*calls)[i]
					require.Equal(s.T(), want.method, got.method)
					require.Equal(s.T(), want.path, got.path)
					require.JSONEq(s.T(), want.body, got.body)
				}
			}
		})
	}
}

func (s *MainSuite) TestPluginInstallReadFails() {
	s.pluginServer(map[string]pluginReply{"POST /api/plugins/inspect": {http.StatusOK, echoFresh}})
	_, err := s.runPlugin(failReader{}, "install", "echo:dev")
	require.EqualError(s.T(), err, "reading the answer: read failed")
}

func (s *MainSuite) TestPluginUpgrade() {
	tests := []struct {
		name     string
		args     []string
		list     pluginReply
		wantErr  string
		wantPull string
	}{
		{name: "latest by default", args: []string{"upgrade", "echo", "-y"}, list: pluginReply{http.StatusOK, echoInstalled}, wantPull: "ghcr.io/acme/echo:latest"},
		{name: "tag", args: []string{"upgrade", "echo", "1.1.0", "-y"}, list: pluginReply{http.StatusOK, echoInstalled}, wantPull: "ghcr.io/acme/echo:1.1.0"},
		{
			name:     "docker hub image",
			args:     []string{"upgrade", "echo", "2", "-y"},
			list:     pluginReply{http.StatusOK, `{"plugins":[{"id":"echo","image":"acme/echo@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}`},
			wantPull: "docker.io/acme/echo:2",
		},
		{name: "not installed", args: []string{"upgrade", "other"}, list: pluginReply{http.StatusOK, echoInstalled}, wantErr: "plugin other is not installed"},
		{
			name:    "dev install",
			args:    []string{"upgrade", "echo"},
			list:    pluginReply{http.StatusOK, `{"plugins":[{"id":"echo","image":"sha256:cc","dev":true}]}`},
			wantErr: "plugin echo is a dev install; install the new image with plugin install --dev",
		},
		{
			name:    "bad image",
			args:    []string{"upgrade", "echo"},
			list:    pluginReply{http.StatusOK, `{"plugins":[{"id":"echo","image":"Not An Image"}]}`},
			wantErr: "reading the plugin's image",
		},
		{name: "list fails", args: []string{"upgrade", "echo"}, list: pluginReply{http.StatusNotImplemented, "disabled\n"}, wantErr: "501 Not Implemented: disabled"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			calls := s.pluginServer(map[string]pluginReply{
				"GET /api/plugins":          tt.list,
				"POST /api/plugins/inspect": {http.StatusOK, echoUpgrade},
				"POST /api/plugins":         {http.StatusOK, echoUpgrade},
			})
			out, err := s.runPlugin(nil, tt.args...)
			if tt.wantErr != "" {
				require.ErrorContains(s.T(), err, tt.wantErr)
				return
			}
			require.NoError(s.T(), err)
			require.Contains(s.T(), out, "Installed echo 1.1.0\n")
			require.Len(s.T(), *calls, 3)
			require.JSONEq(s.T(), `{"dev":false,"image":"`+tt.wantPull+`"}`, (*calls)[1].body)
		})
	}
}

func (s *MainSuite) TestPluginRemove() {
	tests := []struct {
		name    string
		args    []string
		path    string
		reply   pluginReply
		wantOut string
		wantErr string
	}{
		{name: "removed", args: []string{"remove", "echo"}, path: "/api/plugins/echo", reply: pluginReply{http.StatusNoContent, ""}, wantOut: "Removed echo\n"},
		{name: "purged", args: []string{"remove", "echo", "--purge"}, path: "/api/plugins/echo?purge=true", reply: pluginReply{http.StatusNoContent, ""}, wantOut: "Removed echo\n"},
		{name: "not installed", args: []string{"remove", "echo"}, path: "/api/plugins/echo", reply: pluginReply{http.StatusNotFound, "plugin is not installed\n"}, wantErr: "404 Not Found: plugin is not installed"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			s.pluginServer(map[string]pluginReply{"DELETE " + tt.path: tt.reply})
			out, err := s.runPlugin(nil, tt.args...)
			if tt.wantErr != "" {
				require.EqualError(s.T(), err, tt.wantErr)
				return
			}
			require.NoError(s.T(), err)
			require.Equal(s.T(), tt.wantOut, out)
		})
	}
}

func (s *MainSuite) TestPluginRequestErrors() {
	s.Run("bad address", func() {
		s.app.configLoad = func() (*config.Config, error) { return &config.Config{APIAddr: "bad host"}, nil }
		err := s.app.pluginRequest(context.Background(), http.MethodGet, "/api/plugins", nil, http.StatusOK, nil)
		require.ErrorContains(s.T(), err, "building the request")
	})
	s.Run("daemon down", func() {
		s.app.configLoad = func() (*config.Config, error) { return &config.Config{APIAddr: "127.0.0.1:1"}, nil }
		err := s.app.pluginRequest(context.Background(), http.MethodGet, "/api/plugins", nil, http.StatusOK, nil)
		require.ErrorContains(s.T(), err, "calling the plugin API")
	})
	s.Run("body cut short", func() {
		s.uiServer(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "100")
			_, _ = io.WriteString(w, `{"plu`)
		})
		err := s.app.pluginRequest(context.Background(), http.MethodGet, "/api/plugins", nil, http.StatusOK, nil)
		require.ErrorContains(s.T(), err, "reading the answer")
	})
}

func (s *MainSuite) TestPluginDev() {
	dir := filepath.Join(s.T().TempDir(), "My Echo")
	const tag = "loop-plugin-dev/my-echo:latest"
	devFresh := `{"id":"echo","name":"Echo","version":"0.1.0","image":"sha256:cc","capabilities":[],"new_capabilities":[]}`
	tests := []struct {
		name      string
		buildErr  error
		wantCalls []pluginCall
		wantOut   string
		wantErr   string
	}{
		{
			name: "built and installed",
			wantCalls: []pluginCall{
				{http.MethodPost, "/api/plugins/inspect", `{"dev":true,"image":"` + tag + `"}`},
				{http.MethodPost, "/api/plugins", `{"approve":[],"dev":true,"digest":"","image":"` + tag + `"}`},
			},
			wantOut: "building\nEcho 0.1.0 (echo)\nImage: sha256:cc\nCapabilities: none\nIts containers can reach the network.\nInstalled echo 0.1.0\n",
		},
		{
			name:     "build fails",
			buildErr: errors.New("exit status 1"),
			wantErr:  fmt.Sprintf("building %s: exit status 1", dir),
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			calls := s.pluginServer(map[string]pluginReply{
				"POST /api/plugins/inspect": {http.StatusOK, devFresh},
				"POST /api/plugins":         {http.StatusOK, devFresh},
			})
			var built []string
			s.app.runDocker = func(_ context.Context, stdout, _ io.Writer, args ...string) error {
				built = args
				_, _ = io.WriteString(stdout, "building\n")
				return tt.buildErr
			}
			out, err := s.runPlugin(nil, "dev", dir, "--yes")
			require.Equal(s.T(), []string{"build", "-t", tag, dir}, built)
			if tt.wantErr != "" {
				require.EqualError(s.T(), err, tt.wantErr)
				require.Empty(s.T(), *calls)
				return
			}
			require.NoError(s.T(), err)
			require.Equal(s.T(), tt.wantCalls, *calls)
			require.Equal(s.T(), tt.wantOut, out)
		})
	}
}

func (s *MainSuite) TestDevPluginTag() {
	tests := []struct{ dir, want string }{
		{"/src/echo", "loop-plugin-dev/echo:latest"},
		{"/src/My_Plugin.v2", "loop-plugin-dev/my_plugin.v2:latest"},
		{"/src/-x-", "loop-plugin-dev/x:latest"},
		{"/", "loop-plugin-dev/plugin:latest"},
	}
	for _, tt := range tests {
		require.Equal(s.T(), tt.want, devPluginTag(tt.dir), tt.dir)
	}
}

func (s *MainSuite) TestExecRunner() {
	var out bytes.Buffer
	require.NoError(s.T(), execRunner("go")(context.Background(), &out, io.Discard, "version"))
	require.Contains(s.T(), out.String(), "go version")
}
