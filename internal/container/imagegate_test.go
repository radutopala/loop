package container

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/agent"
)

// fakeImageGate is an ImageGate that reports a build in progress when wait
// is set, then returns err.
type fakeImageGate struct {
	wait bool
	err  error
}

func (g fakeImageGate) WaitBuilds(_ context.Context, onWait func()) error {
	if g.wait {
		onWait()
	}
	return g.err
}

func (s *RunnerSuite) TestRunWaitsForImageBuild() {
	ctx := context.Background()
	s.runner.SetImageGate(fakeImageGate{wait: true}, "2026.9.36")
	s.client.On("ImageInspectLabels", ctx, "loop-agent:latest").Return(map[string]string{"loop.version": "2026.9.36"}, nil)
	s.setupMockAttempts(ctx, `{"type":"result","result":"ok","session_id":"sess-1","is_error":false}`)

	var activities [][2]string
	resp, err := s.runner.Run(ctx, &agent.AgentRequest{
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hello"}},
		ChannelID: "ch-1",
		OnActivity: func(activity, detail string) {
			activities = append(activities, [2]string{activity, detail})
		},
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ok", resp.Response)
	require.Equal(s.T(), [][2]string{
		{"image_build", "Waiting for the agent image build to finish"},
		{"image_ready", ""},
	}, activities[:2])
	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunRefusesImage() {
	tests := []struct {
		name    string
		gate    fakeImageGate
		labels  map[string]string
		wantErr string
	}{
		{
			name:    "wait interrupted",
			gate:    fakeImageGate{wait: true, err: context.Canceled},
			wantErr: "waiting for the agent image build: context canceled",
		},
		{
			name:    "built by an older loop",
			labels:  map[string]string{"loop.version": "2026.9.33"},
			wantErr: "image loop-agent:latest is out of date: it was built by loop 2026.9.33, but loop 2026.9.36 is running",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			ctx := context.Background()
			s.runner.SetImageGate(tc.gate, "2026.9.36")
			if tc.labels != nil {
				s.client.On("ImageInspectLabels", ctx, "loop-agent:latest").Return(tc.labels, nil)
			}

			// RunBash passes no activity callback; a waiting gate must cope.
			_, err := s.runner.RunBash(ctx, "true", "ch-1", "", "")
			require.ErrorContains(s.T(), err, tc.wantErr)
			s.client.AssertNotCalled(s.T(), "ContainerCreate", mock.Anything, mock.Anything, mock.Anything)
		})
	}
}

func (s *RunnerSuite) TestRunAcceptsImage() {
	tests := []struct {
		name   string
		labels map[string]string
		err    error
	}{
		{name: "same version", labels: map[string]string{"loop.version": "2026.9.36"}},
		{name: "newer version", labels: map[string]string{"loop.version": "2026.10.1"}},
		{name: "no version label", labels: map[string]string{}},
		{name: "image not inspectable", err: errors.New("no such image")},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			ctx := context.Background()
			s.runner.SetImageGate(fakeImageGate{}, "2026.9.36")
			s.client.On("ImageInspectLabels", ctx, "loop-agent:latest").Return(tc.labels, tc.err)
			s.client.On("ContainerCreate", ctx, mock.AnythingOfType("*container.ContainerConfig"), testContainerName).
				Return("", errors.New("docker create failed"))

			_, err := s.runner.RunBash(ctx, "true", "ch-1", "", "")
			require.ErrorContains(s.T(), err, "creating container")
		})
	}
}

func TestVersionOlder(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"2026.9.33", "2026.9.36", true},
		{"v2026.9.33", "2026.9.36", true},
		{"2026.8.40", "2026.9.1", true},
		{"2025.12.9", "2026.1.1", true},
		{"2026.9.36", "2026.9.36", false},
		{"2026.9.37", "2026.9.36", false},
		{"2026.10.1", "2026.9.36", false},
		{"", "2026.9.36", false},
		{"2026.9.33", "dev", false},
		{"2026.9.33", "2026.9.36-2-gabc1234", false},
		{"2026.9", "2026.9.36", false},
	}
	for _, tc := range tests {
		t.Run(tc.a+"<"+tc.b, func(t *testing.T) {
			require.Equal(t, tc.want, versionOlder(tc.a, tc.b))
		})
	}
}
