package container

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type ChildImagesSuite struct {
	suite.Suite
	client *MockDockerClient
	files  map[string][]byte
	// events records build marks and handoffs, in order.
	events []string
}

func TestChildImagesSuite(t *testing.T) {
	suite.Run(t, new(ChildImagesSuite))
}

func (s *ChildImagesSuite) SetupTest() {
	s.client = new(MockDockerClient)
	s.files = map[string][]byte{}
	s.events = nil
}

// handoff is the RebuildStale handoff, recorded in events.
func (s *ChildImagesSuite) handoff() { s.events = append(s.events, "handoff") }

// beginBuild is the manager's build marker, recorded in events.
func (s *ChildImagesSuite) beginBuild(images ...string) func() {
	s.events = append(s.events, "begin "+strings.Join(images, ","))
	return func() { s.events = append(s.events, "end "+strings.Join(images, ",")) }
}

// newMgr builds a manager over the mock client with an in-memory filesystem
// and a static project list.
func (s *ChildImagesSuite) newMgr(projects []ChildProject, listErr error) *ChildImageManager {
	m := NewChildImageManager(s.client, "loop-agent:latest", func(context.Context) ([]ChildProject, error) {
		return projects, listErr
	}, s.beginBuild, slog.Default())
	m.readFile = func(path string) ([]byte, error) {
		if b, ok := s.files[path]; ok {
			return b, nil
		}
		return nil, errors.New("not found")
	}
	return m
}

func dfPath(dir string) string { return dir + "/.loop/container/Dockerfile" }

func (s *ChildImagesSuite) TestRebuildsStaleChild() {
	s.files[dfPath("/proj")] = []byte("FROM loop-agent:latest\nRUN true\n")
	s.client.On("ImageList", mock.Anything, "loop-agent:latest").Return([]string{"sha256:parent1"}, nil)
	// child labeled with an OLD parent id → stale → rebuild with new label
	s.client.On("ImageInspectLabels", mock.Anything, "proj-agent:latest").
		Return(map[string]string{ParentIDLabel: "sha256:old"}, nil)
	s.client.On("ImageBuildFileLabels", mock.Anything, "/proj/.loop/container", "Dockerfile", "proj-agent:latest",
		map[string]string{ParentIDLabel: "sha256:parent1"}).Return(nil)

	m := s.newMgr([]ChildProject{{DirPath: "/proj", Image: "proj-agent:latest", Autobuild: true}}, nil)
	m.RebuildStale(context.Background(), s.handoff)
	s.client.AssertExpectations(s.T())
}

func (s *ChildImagesSuite) TestSkipsFreshChild() {
	s.files[dfPath("/proj")] = []byte("FROM loop-agent:latest\n")
	s.client.On("ImageList", mock.Anything, "loop-agent:latest").Return([]string{"sha256:parent1"}, nil)
	s.client.On("ImageInspectLabels", mock.Anything, "proj-agent:latest").
		Return(map[string]string{ParentIDLabel: "sha256:parent1"}, nil)

	m := s.newMgr([]ChildProject{{DirPath: "/proj", Image: "proj-agent:latest", Autobuild: true}}, nil)
	m.RebuildStale(context.Background(), s.handoff)
	s.client.AssertNotCalled(s.T(), "ImageBuildFileLabels", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func (s *ChildImagesSuite) TestRebuildsUnlabeledChild() {
	// Pre-feature child images have no parent label → treated as stale once.
	s.files[dfPath("/proj")] = []byte("FROM loop-agent\n")
	s.client.On("ImageList", mock.Anything, "loop-agent:latest").Return([]string{"sha256:p"}, nil)
	s.client.On("ImageInspectLabels", mock.Anything, "proj-agent:latest").
		Return(map[string]string{}, nil)
	s.client.On("ImageBuildFileLabels", mock.Anything, "/proj/.loop/container", "Dockerfile", "proj-agent:latest",
		map[string]string{ParentIDLabel: "sha256:p"}).Return(nil)

	m := s.newMgr([]ChildProject{{DirPath: "/proj", Image: "proj-agent:latest", Autobuild: true}}, nil)
	m.RebuildStale(context.Background(), s.handoff)
	s.client.AssertExpectations(s.T())
}

func (s *ChildImagesSuite) TestInspectErrorTreatedAsStale() {
	// A child image that doesn't exist yet inspects with an error → build it.
	s.files[dfPath("/proj")] = []byte("FROM loop-agent:latest\n")
	s.client.On("ImageList", mock.Anything, "loop-agent:latest").Return([]string{"sha256:p"}, nil)
	s.client.On("ImageInspectLabels", mock.Anything, "proj-agent:latest").
		Return(map[string]string(nil), errors.New("no such image"))
	s.client.On("ImageBuildFileLabels", mock.Anything, "/proj/.loop/container", "Dockerfile", "proj-agent:latest",
		map[string]string{ParentIDLabel: "sha256:p"}).Return(nil)

	m := s.newMgr([]ChildProject{{DirPath: "/proj", Image: "proj-agent:latest", Autobuild: true}}, nil)
	m.RebuildStale(context.Background(), s.handoff)
	s.client.AssertExpectations(s.T())
}

func (s *ChildImagesSuite) TestBuildErrorContinuesToNextChild() {
	s.files[dfPath("/a")] = []byte("FROM loop-agent:latest\n")
	s.files[dfPath("/b")] = []byte("FROM loop-agent:latest\n")
	s.client.On("ImageList", mock.Anything, "loop-agent:latest").Return([]string{"sha256:p"}, nil)
	s.client.On("ImageInspectLabels", mock.Anything, mock.Anything).Return(map[string]string{}, nil)
	s.client.On("ImageBuildFileLabels", mock.Anything, "/a/.loop/container", "Dockerfile", "a-agent:latest", mock.Anything).
		Return(errors.New("build boom"))
	s.client.On("ImageBuildFileLabels", mock.Anything, "/b/.loop/container", "Dockerfile", "b-agent:latest", mock.Anything).
		Return(nil)

	m := s.newMgr([]ChildProject{
		{DirPath: "/a", Image: "a-agent:latest", Autobuild: true},
		{DirPath: "/b", Image: "b-agent:latest", Autobuild: true},
	}, nil)
	m.RebuildStale(context.Background(), s.handoff)
	s.client.AssertExpectations(s.T())
	// Every stale child is marked before the base build hands over, and each
	// is released as soon as its own build is done, failed or not.
	require.Equal(s.T(), []string{
		"begin a-agent:latest", "begin b-agent:latest", "handoff",
		"end a-agent:latest", "end b-agent:latest",
	}, s.events)
}

// The base build must not hand over before the child is marked, or a run
// could start on the old project image in between; the mark lasts until the
// child's build is done.
func (s *ChildImagesSuite) TestMarksChildBeforeHandoffUntilBuilt() {
	s.files[dfPath("/proj")] = []byte("FROM loop-agent:latest\n")
	s.client.On("ImageList", mock.Anything, "loop-agent:latest").Return([]string{"sha256:p"}, nil)
	s.client.On("ImageInspectLabels", mock.Anything, "proj-agent:latest").Return(map[string]string{}, nil)
	s.client.On("ImageBuildFileLabels", mock.Anything, "/proj/.loop/container", "Dockerfile", "proj-agent:latest", mock.Anything).
		Run(func(mock.Arguments) { s.events = append(s.events, "build proj-agent:latest") }).
		Return(nil)

	m := s.newMgr([]ChildProject{{DirPath: "/proj", Image: "proj-agent:latest", Autobuild: true}}, nil)
	m.RebuildStale(context.Background(), s.handoff)
	require.Equal(s.T(), []string{
		"begin proj-agent:latest", "handoff", "build proj-agent:latest", "end proj-agent:latest",
	}, s.events)
}

// However the cascade ends early, the base build is handed over exactly once.
func (s *ChildImagesSuite) TestHandoffWhenNothingToRebuild() {
	tests := []struct {
		name  string
		setup func(m *ChildImageManager)
	}{
		{name: "no base image name", setup: func(m *ChildImageManager) { m.baseImage = "" }},
		{name: "base image missing", setup: func(*ChildImageManager) {
			s.client.On("ImageList", mock.Anything, "loop-agent:latest").Return([]string{}, nil)
		}},
		{name: "listing projects fails", setup: func(m *ChildImageManager) {
			s.client.On("ImageList", mock.Anything, "loop-agent:latest").Return([]string{"sha256:p"}, nil)
			m.listProjects = func(context.Context) ([]ChildProject, error) { return nil, errors.New("db down") }
		}},
		{name: "no stale child", setup: func(*ChildImageManager) {
			s.client.On("ImageList", mock.Anything, "loop-agent:latest").Return([]string{"sha256:p"}, nil)
		}},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			m := s.newMgr(nil, nil)
			tc.setup(m)
			m.RebuildStale(context.Background(), s.handoff)
			require.Equal(s.T(), []string{"handoff"}, s.events)
		})
	}
}

func (s *ChildImagesSuite) TestSkipsIneligibleProjects() {
	// no override / same-as-base / autobuild off / duplicate image / no
	// Dockerfile / Dockerfile from another base — none may trigger a build.
	s.files[dfPath("/foreign")] = []byte("FROM ubuntu:24.04\n")
	s.files[dfPath("/dup")] = []byte("FROM loop-agent:latest\n")
	s.client.On("ImageList", mock.Anything, "loop-agent:latest").Return([]string{"sha256:p"}, nil)
	s.client.On("ImageInspectLabels", mock.Anything, "dup-agent:latest").
		Return(map[string]string{ParentIDLabel: "sha256:p"}, nil)

	m := s.newMgr([]ChildProject{
		{DirPath: "/none", Image: "", Autobuild: true},
		{DirPath: "/base", Image: "loop-agent:latest", Autobuild: true},
		{DirPath: "/optout", Image: "opt-agent:latest", Autobuild: false},
		{DirPath: "/dup", Image: "dup-agent:latest", Autobuild: true},
		{DirPath: "/dup2", Image: "dup-agent:latest", Autobuild: true},
		{DirPath: "/nodf", Image: "nodf-agent:latest", Autobuild: true},
		{DirPath: "/foreign", Image: "foreign-agent:latest", Autobuild: true},
	}, nil)
	m.RebuildStale(context.Background(), s.handoff)
	s.client.AssertNotCalled(s.T(), "ImageBuildFileLabels", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func (s *ChildImagesSuite) TestNoBaseImageSkipsCascade() {
	s.client.On("ImageList", mock.Anything, "loop-agent:latest").Return([]string{}, nil)
	m := s.newMgr([]ChildProject{{DirPath: "/proj", Image: "x:latest", Autobuild: true}}, nil)
	m.RebuildStale(context.Background(), s.handoff)
	s.client.AssertNotCalled(s.T(), "ImageInspectLabels", mock.Anything, mock.Anything)
}

func (s *ChildImagesSuite) TestBaseImageListErrorSkipsCascade() {
	s.client.On("ImageList", mock.Anything, "loop-agent:latest").Return([]string(nil), errors.New("docker down"))
	m := s.newMgr(nil, nil)
	m.RebuildStale(context.Background(), s.handoff)
}

func (s *ChildImagesSuite) TestListProjectsErrorSkipsCascade() {
	s.client.On("ImageList", mock.Anything, "loop-agent:latest").Return([]string{"sha256:p"}, nil)
	m := s.newMgr(nil, errors.New("db down"))
	m.RebuildStale(context.Background(), s.handoff)
	s.client.AssertNotCalled(s.T(), "ImageInspectLabels", mock.Anything, mock.Anything)
}

func (s *ChildImagesSuite) TestNewManagerDefaultsReadFile() {
	m := NewChildImageManager(s.client, "loop-agent:latest", nil, nil, slog.Default())
	require.NotNil(s.T(), m.readFile)
	_, err := m.readFile("/definitely/not/a/file")
	require.Error(s.T(), err)
}

func TestDockerfileFromBase(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{"plain match", "FROM loop-agent:latest\n", true},
		{"untagged match", "FROM loop-agent\n", true},
		{"alias stage", "FROM loop-agent:latest AS base\nFROM scratch\n", true},
		{"platform flag", "FROM --platform=linux/amd64 loop-agent:latest\n", true},
		{"case insensitive", "from loop-agent:latest\n", true},
		{"later stage", "FROM golang:1.26 AS build\nFROM loop-agent:latest\n", true},
		{"other base", "FROM ubuntu:24.04\n", false},
		{"other tag", "FROM loop-agent:v1\n", false},
		{"comment only", "# FROM loop-agent:latest is not an instruction? it is a comment line\n", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, dockerfileFromBase(tc.content, "loop-agent:latest"))
		})
	}
}

func (s *ChildImagesSuite) TestEmptyBaseImageNoop() {
	m := s.newMgr(nil, nil)
	m.baseImage = ""
	m.RebuildStale(context.Background(), s.handoff)
	s.client.AssertNotCalled(s.T(), "ImageList", mock.Anything, mock.Anything)
}
