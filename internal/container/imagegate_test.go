package container

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/agent"
)

// fakeImageGate is an ImageGate that reports builds of the images in
// building in progress, in turn, then returns err. It records the images it
// was asked to wait for.
type fakeImageGate struct {
	building []string
	err      error
	images   []string
}

func (g *fakeImageGate) WaitBuilds(_ context.Context, images []string, onWait func(string)) error {
	g.images = images
	for _, image := range g.building {
		onWait(image)
	}
	return g.err
}

// fakeMissingImageBuilder is a MissingImageBuilder that reports a build when
// build is set, then returns err. It records what it was asked to build.
type fakeMissingImageBuilder struct {
	build     bool
	err       error
	image     string
	autobuild bool
	dirs      []string
}

func (b *fakeMissingImageBuilder) BuildMissing(_ context.Context, image string, autobuild bool, dirs []string, onBuild func()) error {
	b.image, b.autobuild, b.dirs = image, autobuild, dirs
	if b.build {
		onBuild()
	}
	return b.err
}

func (s *RunnerSuite) TestRunBuildsMissingImage() {
	tests := []struct {
		name           string
		gate           *fakeImageGate
		builder        *fakeMissingImageBuilder
		wantActivities [][2]string
	}{
		{
			name:    "builds without a gate",
			builder: &fakeMissingImageBuilder{build: true},
			wantActivities: [][2]string{
				{"image_build", "Building the loop-agent:latest image"},
				{"image_ready", ""},
			},
		},
		{
			name:    "waits for the base, then builds",
			gate:    &fakeImageGate{building: []string{"loop-agent:base"}},
			builder: &fakeMissingImageBuilder{build: true},
			wantActivities: [][2]string{
				{"image_build", "Waiting for the loop-agent:base image build to finish"},
				{"image_build", "Building the loop-agent:latest image"},
				{"image_ready", ""},
			},
		},
		{
			name:    "nothing to build",
			builder: &fakeMissingImageBuilder{},
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			ctx := context.Background()
			if tc.gate != nil {
				s.runner.SetImageGate(tc.gate, "2026.9.36", "loop-agent:base")
				s.client.On("ImageInspectLabels", ctx, "loop-agent:latest").Return(map[string]string{}, nil)
			}
			s.runner.SetMissingImageBuilder(tc.builder)
			s.setupMockAttempts(ctx, `{"type":"result","result":"ok","session_id":"sess-1","is_error":false}`)

			var activities [][2]string
			resp, err := s.runner.Run(ctx, &agent.AgentRequest{
				Messages:  []agent.AgentMessage{{Role: "user", Content: "hello"}},
				ChannelID: "ch-1",
				OnActivity: func(activity, detail string) {
					if activity == "image_build" || activity == "image_ready" {
						activities = append(activities, [2]string{activity, detail})
					}
				},
			})
			require.NoError(s.T(), err)
			require.Equal(s.T(), "ok", resp.Response)
			require.Equal(s.T(), tc.wantActivities, activities)
			require.Equal(s.T(), "loop-agent:latest", tc.builder.image)
			// The run's own dir, then the parent project's (none here).
			require.Len(s.T(), tc.builder.dirs, 2)
			require.NotEmpty(s.T(), tc.builder.dirs[0])
			require.Empty(s.T(), tc.builder.dirs[1])
		})
	}
}

func (s *RunnerSuite) TestRunFailsMissingImageBuild() {
	ctx := context.Background()
	s.runner.SetMissingImageBuilder(&fakeMissingImageBuilder{build: true, err: errors.New("building image loop-agent:latest: RUN failed")})

	_, err := s.runner.RunBash(ctx, "true", "ch-1", "", "")
	require.ErrorContains(s.T(), err, "building image loop-agent:latest: RUN failed")
	s.client.AssertNotCalled(s.T(), "ContainerCreate", mock.Anything, mock.Anything, mock.Anything)
}

func (s *RunnerSuite) TestRunWaitsForImageBuild() {
	ctx := context.Background()
	gate := &fakeImageGate{building: []string{"loop-agent:base", "loop-agent:latest"}}
	s.runner.SetImageGate(gate, "2026.9.36", "loop-agent:base")
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
	// The run waits for its own image and the base it is built FROM, and
	// says which one it is waiting for.
	require.Equal(s.T(), []string{"loop-agent:latest", "loop-agent:base"}, gate.images)
	require.Equal(s.T(), [][2]string{
		{"image_build", "Waiting for the loop-agent:base image build to finish"},
		{"image_build", "Waiting for the loop-agent:latest image build to finish"},
		{"image_ready", ""},
	}, activities[:3])
	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunRefusesImage() {
	tests := []struct {
		name    string
		gate    *fakeImageGate
		labels  map[string]string
		wantErr string
	}{
		{
			name:    "wait interrupted",
			gate:    &fakeImageGate{building: []string{"loop-agent:latest"}, err: context.Canceled},
			wantErr: "waiting for the image build: context canceled",
		},
		{
			name:    "built by an older loop",
			gate:    &fakeImageGate{},
			labels:  map[string]string{"loop.version": "2026.9.33"},
			wantErr: "image loop-agent:latest is out of date: it was built by loop 2026.9.33, but loop 2026.9.36 is running",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			ctx := context.Background()
			s.runner.SetImageGate(tc.gate, "2026.9.36", "loop-agent:latest")
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
			s.runner.SetImageGate(&fakeImageGate{}, "2026.9.36", "loop-agent:latest")
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
