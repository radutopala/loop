package container

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/testutil"
)

// copiedFile is one tar entry a test saw copied into a container.
type copiedFile struct {
	data     string
	mode     int64
	uid, gid int
	isDir    bool
}

// copiedFiles gathers the tar entries of every CopyToContainer call to "/"
// recorded on client, keyed by entry name.
func copiedFiles(t *testing.T, client *MockDockerClient) map[string]copiedFile {
	t.Helper()
	out := map[string]copiedFile{}
	for _, c := range client.Calls {
		if c.Method != "CopyToContainer" || c.Arguments.String(2) != "/" {
			continue
		}
		buf := c.Arguments.Get(3).(*bytes.Buffer)
		tr := tar.NewReader(bytes.NewReader(buf.Bytes()))
		for {
			h, err := tr.Next()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			data, err := io.ReadAll(tr)
			require.NoError(t, err)
			out[h.Name] = copiedFile{data: string(data), mode: h.Mode, uid: h.Uid, gid: h.Gid, isDir: h.Typeflag == tar.TypeDir}
		}
	}
	return out
}

// fakeIssuer is a TokenIssuer recording its calls.
type fakeIssuer struct {
	token   string
	err     error
	issued  [][3]string
	revoked []string
}

func (f *fakeIssuer) Issue(containerID, channelID, dirPath string) (string, error) {
	f.issued = append(f.issued, [3]string{containerID, channelID, dirPath})
	return f.token, f.err
}

func (f *fakeIssuer) Revoke(containerID string) {
	f.revoked = append(f.revoked, containerID)
}

type TokensSuite struct {
	suite.Suite
	client *MockDockerClient
	sys    *testutil.MockSystem
	runner *DockerRunner
}

func TestTokensSuite(t *testing.T) {
	suite.Run(t, new(TokensSuite))
}

func (s *TokensSuite) SetupTest() {
	s.client = new(MockDockerClient)
	s.sys = new(testutil.MockSystem)
	s.sys.On("Getuid").Return(1001).Maybe()
	s.sys.On("Getgid").Return(1002).Maybe()
	s.runner = NewDockerRunner(s.client, &config.Config{}, nil)
	s.runner.sys = s.sys
}

func (s *TokensSuite) TestWriteRunTokens() {
	tests := []struct {
		name      string
		gate, api string
		want      map[string]copiedFile
	}{
		{
			name: "both",
			gate: "g-tok",
			api:  "a-tok",
			want: map[string]copiedFile{
				"run/":                {mode: 0o755, isDir: true},
				"run/loop/":           {mode: 0o755, isDir: true},
				"run/loop/gate-token": {data: "g-tok", mode: 0o400},
				"run/loop/api-token":  {data: "a-tok", mode: 0o400, uid: 1001, gid: 1002},
			},
		},
		{
			name: "api only",
			api:  "a-tok",
			want: map[string]copiedFile{
				"run/":               {mode: 0o755, isDir: true},
				"run/loop/":          {mode: 0o755, isDir: true},
				"run/loop/api-token": {data: "a-tok", mode: 0o400, uid: 1001, gid: 1002},
			},
		},
		{
			name: "gate only",
			gate: "g-tok",
			want: map[string]copiedFile{
				"run/":                {mode: 0o755, isDir: true},
				"run/loop/":           {mode: 0o755, isDir: true},
				"run/loop/gate-token": {data: "g-tok", mode: 0o400},
			},
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			ctx := context.Background()
			s.client.On("CopyToContainer", ctx, "cid", "/", mock.Anything).Return(nil).Once()
			s.Require().NoError(s.runner.writeRunTokens(ctx, "cid", tt.gate, tt.api))
			s.Require().Equal(tt.want, copiedFiles(s.T(), s.client))
		})
	}
}

func (s *TokensSuite) TestWriteRunTokensNothingToWrite() {
	s.Require().NoError(s.runner.writeRunTokens(context.Background(), "cid", "", ""))
	s.client.AssertNotCalled(s.T(), "CopyToContainer", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func (s *TokensSuite) TestWriteRunTokensCopyError() {
	s.client.On("CopyToContainer", mock.Anything, "cid", "/", mock.Anything).Return(errors.New("boom"))
	s.Require().EqualError(s.runner.writeRunTokens(context.Background(), "cid", "g", ""), "boom")
}

func (s *TokensSuite) TestContainerRemoveRevokes() {
	issuer := &fakeIssuer{}
	s.runner.SetTokenIssuer(issuer)
	s.client.On("ContainerRemove", mock.Anything, "cid").Return(nil)
	s.Require().NoError(s.runner.ContainerRemove(context.Background(), "cid"))
	s.Require().Equal([]string{"cid"}, issuer.revoked)
}

// TestRunIssuesAPIToken: a spawn mints an API token for the container and
// copies it in before start.
func (s *RunnerSuite) TestRunIssuesAPIToken() {
	issuer := &fakeIssuer{token: "a-tok"}
	s.runner.SetTokenIssuer(issuer)

	ctx := context.Background()
	s.setupMockRun(ctx, mock.Anything, mock.Anything, testJSONOK)
	s.client.On("CopyToContainer", ctx, testContainerID, "/", mock.Anything).Return(nil)

	_, err := s.runner.Run(ctx, &agent.AgentRequest{ChannelID: "ch-1", DirPath: "/proj"})
	s.Require().NoError(err)
	s.Require().Equal([][3]string{{testContainerID, "ch-1", "/proj"}}, issuer.issued)
	s.Require().Equal("a-tok", copiedFiles(s.T(), s.client)["run/loop/api-token"].data)
}

func (s *RunnerSuite) TestRunTokenErrors() {
	tests := []struct {
		name    string
		issuer  *fakeIssuer
		copyErr error
		wantErr string
	}{
		{name: "issue", issuer: &fakeIssuer{err: errors.New("no store")}, wantErr: "issuing api token: no store"},
		{name: "copy", issuer: &fakeIssuer{token: "a-tok"}, copyErr: errors.New("disk full"), wantErr: "writing tokens: disk full"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			s.runner.SetTokenIssuer(tt.issuer)
			ctx := context.Background()
			s.client.On("ContainerCreate", ctx, mock.Anything, mock.Anything).Return(testContainerID, nil)
			s.client.On("CopyToContainer", ctx, testContainerID, "/", mock.Anything).Return(tt.copyErr).Maybe()

			_, err := s.runner.Run(ctx, &agent.AgentRequest{ChannelID: "ch-1"})
			s.Require().ErrorContains(err, tt.wantErr)
			s.client.AssertNotCalled(s.T(), "ContainerStart", mock.Anything, mock.Anything)
		})
	}
}
