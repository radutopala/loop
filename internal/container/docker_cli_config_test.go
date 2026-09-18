package container

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func (s *RunnerSuite) TestDockerCLIConfigDir() {
	tests := []struct {
		name string
		env  []string
		want string
	}{
		{
			name: "defaults to HOME/.docker",
			env:  []string{"HOME=/home/agent", "PATH=/bin"},
			want: "/home/agent/.docker",
		},
		{
			name: "DOCKER_CONFIG wins over HOME",
			env:  []string{"HOME=/home/agent", "DOCKER_CONFIG=/etc/docker-cli"},
			want: "/etc/docker-cli",
		},
		{
			name: "DOCKER_CONFIG before HOME still wins",
			env:  []string{"DOCKER_CONFIG=/etc/docker-cli", "HOME=/home/agent"},
			want: "/etc/docker-cli",
		},
		{
			name: "last duplicate wins, as Docker resolves it",
			env:  []string{"HOME=/first", "HOME=/second"},
			want: "/second/.docker",
		},
		{
			name: "empty DOCKER_CONFIG falls back to HOME",
			env:  []string{"HOME=/home/agent", "DOCKER_CONFIG="},
			want: "/home/agent/.docker",
		},
		{
			name: "neither set",
			env:  []string{"PATH=/bin"},
			want: "",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.want, dockerCLIConfigDir(tc.env))
		})
	}
}

func (s *RunnerSuite) TestDockerCLIProxyConfig() {
	const noProxy = "host.docker.internal,localhost,127.0.0.1,::1,172.16.0.0/12"
	proxyEnv := []string{
		"HTTP_PROXY=http://host.docker.internal:3128",
		"HTTPS_PROXY=http://host.docker.internal:3128",
		"NO_PROXY=" + noProxy,
	}

	tests := []struct {
		name string
		env  []string
		// want is the expected decoded config; nil means "write nothing".
		want map[string]any
	}{
		{
			name: "no proxy configured writes nothing",
			env:  []string{"HOME=/home/agent", "NO_PROXY=localhost"},
			want: nil,
		},
		{
			name: "all three values",
			env:  proxyEnv,
			want: map[string]any{"proxies": map[string]any{"default": map[string]any{
				"httpProxy":  "http://host.docker.internal:3128",
				"httpsProxy": "http://host.docker.internal:3128",
				"noProxy":    noProxy,
			}}},
		},
		{
			name: "https-only proxy still writes",
			env:  []string{"HTTPS_PROXY=http://host.docker.internal:3128"},
			want: map[string]any{"proxies": map[string]any{"default": map[string]any{
				"httpsProxy": "http://host.docker.internal:3128",
			}}},
		},
		{
			name: "lowercase-only env is honored",
			env:  []string{"http_proxy=http://host.docker.internal:3128", "no_proxy=" + noProxy},
			want: map[string]any{"proxies": map[string]any{"default": map[string]any{
				"httpProxy": "http://host.docker.internal:3128",
				"noProxy":   noProxy,
			}}},
		},
		{
			name: "entries without a separator are ignored",
			env:  []string{"MALFORMED", "HTTP_PROXY=http://host.docker.internal:3128"},
			want: map[string]any{"proxies": map[string]any{"default": map[string]any{
				"httpProxy": "http://host.docker.internal:3128",
			}}},
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			got := dockerCLIProxyConfig(tc.env)
			if tc.want == nil {
				require.Nil(s.T(), got)
				return
			}
			var decoded map[string]any
			require.NoError(s.T(), json.Unmarshal(got, &decoded))
			require.Equal(s.T(), tc.want, decoded)
		})
	}
}

// The values handed to the CLI have to be the ones the container itself got,
// localhost already rewritten — a nested container cannot reach a proxy on the
// agent container's own loopback.
func (s *RunnerSuite) TestDockerCLIProxyConfigRewritesLocalhost() {
	env := map[string]string{"HTTP_PROXY": "http://localhost:3128", "HTTPS_PROXY": "https://127.0.0.1:3128"}
	data := dockerCLIProxyConfig(ProxyEnv(ProxySettings{}, func(k string) string { return env[k] }))

	var decoded struct {
		Proxies struct {
			Default struct {
				HTTPProxy  string `json:"httpProxy"`
				HTTPSProxy string `json:"httpsProxy"`
				NoProxy    string `json:"noProxy"`
			} `json:"default"`
		} `json:"proxies"`
	}
	require.NoError(s.T(), json.Unmarshal(data, &decoded))
	require.Equal(s.T(), "http://host.docker.internal:3128", decoded.Proxies.Default.HTTPProxy)
	require.Equal(s.T(), "https://host.docker.internal:3128", decoded.Proxies.Default.HTTPSProxy)
	require.Equal(s.T(), "host.docker.internal,localhost,127.0.0.1,::1,172.16.0.0/12", decoded.Proxies.Default.NoProxy)
}

// tarEntries reads a tar stream into name -> header/body pairs.
func tarEntries(t *testing.T, r io.Reader) map[string]struct {
	Header *tar.Header
	Body   string
} {
	t.Helper()
	out := map[string]struct {
		Header *tar.Header
		Body   string
	}{}
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		require.NoError(t, err)
		body, err := io.ReadAll(tr)
		require.NoError(t, err)
		out[h.Name] = struct {
			Header *tar.Header
			Body   string
		}{h, string(body)}
	}
}

func (s *RunnerSuite) TestAncestorDirs() {
	tests := []struct {
		name string
		dir  string
		want []string
	}{
		{name: "home dir", dir: "/home/agent/.docker", want: []string{"home", "home/agent"}},
		{
			name: "host-mirrored home, as the agent container uses",
			dir:  "/Users/someone/.docker",
			want: []string{"Users", "Users/someone"},
		},
		{name: "trailing slash", dir: "/etc/docker-cli/", want: []string{"etc"}},
		{name: "single level has no ancestor below root", dir: "/dockercfg", want: nil},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			require.Equal(s.T(), tt.want, ancestorDirs(tt.dir))
		})
	}
}

func (s *RunnerSuite) TestWriteDockerCLIConfig() {
	ctx := context.Background()
	env := []string{
		"HOME=/home/agent",
		"HTTP_PROXY=http://host.docker.internal:3128",
		"NO_PROXY=host.docker.internal",
	}

	s.sys.Override("Getuid").Return(501)
	s.sys.Override("Getgid").Return(20)

	var got []byte
	var dst string
	s.client.On("CopyToContainer", ctx, testContainerID, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			dst = args.String(2)
			var err error
			got, err = io.ReadAll(args.Get(3).(io.Reader))
			require.NoError(s.T(), err)
		}).Return(nil)

	require.NoError(s.T(), s.runner.writeDockerCLIConfig(ctx, testContainerID, env))

	// Extracted at / with every ancestor spelled out: the daemon refuses a
	// destination that does not exist, and $HOME often does not exist yet.
	require.Equal(s.T(), "/", dst)
	entries := tarEntries(s.T(), bytes.NewReader(got))
	require.Len(s.T(), entries, 4)

	for _, name := range []string{"home/", "home/agent/"} {
		ancestor := entries[name]
		require.Equal(s.T(), byte(tar.TypeDir), ancestor.Header.Typeflag, name)
		require.Equal(s.T(), int64(0755), ancestor.Header.Mode, name)
		require.Equal(s.T(), 501, ancestor.Header.Uid, name)
		require.Equal(s.T(), 20, ancestor.Header.Gid, name)
	}

	dir := entries["home/agent/.docker/"]
	require.Equal(s.T(), byte(tar.TypeDir), dir.Header.Typeflag)
	require.Equal(s.T(), int64(0700), dir.Header.Mode)
	require.Equal(s.T(), 501, dir.Header.Uid)
	require.Equal(s.T(), 20, dir.Header.Gid)

	file := entries["home/agent/.docker/config.json"]
	require.Equal(s.T(), int64(0600), file.Header.Mode)
	require.Equal(s.T(), 501, file.Header.Uid)
	require.Equal(s.T(), 20, file.Header.Gid)

	var decoded map[string]any
	require.NoError(s.T(), json.Unmarshal([]byte(file.Body), &decoded))
	require.Equal(s.T(), map[string]any{"default": map[string]any{
		"httpProxy": "http://host.docker.internal:3128",
		"noProxy":   "host.docker.internal",
	}}, decoded["proxies"])

	s.client.AssertExpectations(s.T())
}

// The no-proxy case has to stay exactly as it was: no file, no copy, nothing
// for the Docker CLI to find.
func (s *RunnerSuite) TestWriteDockerCLIConfigSkipped() {
	ctx := context.Background()
	tests := []struct {
		name string
		env  []string
	}{
		{name: "no proxy configured", env: []string{"HOME=/home/agent"}},
		{name: "no proxy, only a bypass list", env: []string{"HOME=/home/agent", "NO_PROXY=localhost"}},
		{name: "no config dir to write to", env: []string{"HTTP_PROXY=http://host.docker.internal:3128"}},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			client := new(MockDockerClient)
			runner := &DockerRunner{client: client, sys: newDefaultMockSystem()}

			require.NoError(s.T(), runner.writeDockerCLIConfig(ctx, testContainerID, tc.env))
			client.AssertNotCalled(s.T(), "CopyToContainer", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
		})
	}
}

func (s *RunnerSuite) TestWriteDockerCLIConfigErrors() {
	ctx := context.Background()
	env := []string{"HOME=/home/agent", "HTTP_PROXY=http://host.docker.internal:3128"}

	tests := []struct {
		name    string
		setup   func(*MockDockerClient)
		wantErr string
	}{
		{
			name: "copy fails",
			setup: func(client *MockDockerClient) {
				client.On("CopyToContainer", ctx, testContainerID, mock.Anything, mock.Anything).
					Return(errors.New("no such directory"))
			},
			wantErr: "no such directory",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			client := new(MockDockerClient)
			tc.setup(client)
			runner := &DockerRunner{client: client, sys: newDefaultMockSystem()}

			err := runner.writeDockerCLIConfig(ctx, testContainerID, env)
			require.ErrorContains(s.T(), err, tc.wantErr)
		})
	}
}

// The host's Docker CLI config must never reach the container. It carries
// machine-local keys: currentContext names a Docker Desktop context that has no
// contexts/ directory inside the container, which makes the CLI abort before it
// runs anything ("unable to resolve docker endpoint"), and credsStore names a
// helper binary that is not on the container's PATH, which fails any pull that
// needs a credential lookup.
func (s *RunnerSuite) TestWriteDockerCLIConfigIgnoresHostConfig() {
	ctx := context.Background()
	const configPath = "/home/agent/.docker/config.json"
	env := []string{"HOME=/home/agent", "HTTP_PROXY=http://host.docker.internal:3128"}

	// A host config sitting at the very path the container reads from: the
	// container mirrors the host home path, so the two names coincide.
	s.sys.Override("ReadFile", configPath).Return([]byte(`{
		"credsStore": "desktop",
		"currentContext": "desktop-linux",
		"auths": {"ghcr.io": {}}
	}`), nil)
	var got []byte
	s.client.On("CopyToContainer", ctx, testContainerID, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			var err error
			got, err = io.ReadAll(args.Get(3).(io.Reader))
			require.NoError(s.T(), err)
		}).Return(nil)

	require.NoError(s.T(), s.runner.writeDockerCLIConfig(ctx, testContainerID, env))

	entries := tarEntries(s.T(), bytes.NewReader(got))
	var decoded map[string]any
	require.NoError(s.T(), json.Unmarshal([]byte(entries["home/agent/.docker/config.json"].Body), &decoded))

	require.NotContains(s.T(), decoded, "currentContext")
	require.NotContains(s.T(), decoded, "credsStore")
	require.NotContains(s.T(), decoded, "auths")
	// Proxies is the only key ever written.
	require.Equal(s.T(), []string{"proxies"}, sortedKeys(decoded))
	s.sys.AssertNotCalled(s.T(), "ReadFile", configPath)
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
