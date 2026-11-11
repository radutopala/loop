package localserver

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type mockSystem struct{ mock.Mock }

func (m *mockSystem) Executable() (string, error) {
	args := m.Called()
	return args.String(0), args.Error(1)
}

func (m *mockSystem) Stat(name string) (fs.FileInfo, error) {
	args := m.Called(name)
	info, _ := args.Get(0).(fs.FileInfo)
	return info, args.Error(1)
}

func (m *mockSystem) LookPath(file string) (string, error) {
	args := m.Called(file)
	return args.String(0), args.Error(1)
}

func (m *mockSystem) ReadFile(name string) ([]byte, error) {
	args := m.Called(name)
	data, _ := args.Get(0).([]byte)
	return data, args.Error(1)
}

func (m *mockSystem) WriteFile(name string, data []byte, perm os.FileMode) error {
	return m.Called(name, string(data), perm).Error(0)
}

func (m *mockSystem) Remove(name string) error { return m.Called(name).Error(0) }

func (m *mockSystem) FindProcess(pid int) (Process, error) {
	args := m.Called(pid)
	p, _ := args.Get(0).(Process)
	return p, args.Error(1)
}

func (m *mockSystem) Start(name string, args []string, out io.Writer) (Process, error) {
	a := m.Called(name, args, out)
	p, _ := a.Get(0).(Process)
	return p, a.Error(1)
}

func (m *mockSystem) FreePort() (int, error) {
	args := m.Called()
	return args.Int(0), args.Error(1)
}

type mockHTTP struct{ mock.Mock }

func (m *mockHTTP) Do(req *http.Request) (*http.Response, error) {
	args := m.Called(req.URL.String())
	resp, _ := args.Get(0).(*http.Response)
	return resp, args.Error(1)
}

// fakeProc is a process that runs until killed or exit is called.
type fakeProc struct {
	pid    int
	once   sync.Once
	done   chan struct{}
	killed bool
}

func newFakeProc(pid int) *fakeProc { return &fakeProc{pid: pid, done: make(chan struct{})} }

func (p *fakeProc) Pid() int { return p.pid }
func (p *fakeProc) Wait() error {
	<-p.done
	return nil
}
func (p *fakeProc) Kill() error {
	p.killed = true
	p.exit()
	return nil
}
func (p *fakeProc) exit() { p.once.Do(func() { close(p.done) }) }

type fileInfo struct {
	fs.FileInfo
	dir bool
}

func (f fileInfo) IsDir() bool { return f.dir }

func ok() *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok"))}
}

func status(code int) *http.Response {
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(""))}
}

type LocalServerSuite struct {
	suite.Suite
	sys  *mockSystem
	http *mockHTTP
	srv  *Server
	now  time.Time
}

func TestLocalServerSuite(t *testing.T) {
	suite.Run(t, new(LocalServerSuite))
}

func (s *LocalServerSuite) SetupTest() {
	s.sys = new(mockSystem)
	s.http = new(mockHTTP)
	s.now = time.Unix(1000, 0)
	s.srv = New(Config{
		Binary:         "srv",
		Args:           func(port int) []string { return []string{"--port", strconv.Itoa(port)} },
		HealthPath:     "/health",
		PidFile:        "/run/srv.pid",
		IdleStop:       time.Minute,
		StartupTimeout: time.Second,
	}, s.sys, s.http, slog.New(slog.DiscardHandler))
	s.srv.now = func() time.Time { return s.now }
	s.srv.pollDelay = time.Millisecond
}

// bundled makes FindBinary find the binary next to the executable.
func (s *LocalServerSuite) bundled() {
	s.sys.On("Executable").Return("/app/bin/loop", nil)
	s.sys.On("Stat", "/app/bin/srv").Return(fileInfo{}, nil)
}

func (s *LocalServerSuite) TestExeName() {
	require.Equal(s.T(), "srv.exe", ExeName("srv", "windows"))
	require.Equal(s.T(), "srv", ExeName("srv", "darwin"))
}

func (s *LocalServerSuite) TestFindBinary() {
	tests := []struct {
		name  string
		setup func()
		want  string
		err   bool
	}{
		{name: "next to the executable", setup: s.bundled, want: "/app/bin/srv"},
		{
			name: "a directory there falls back to PATH",
			setup: func() {
				s.sys.On("Executable").Return("/app/bin/loop", nil)
				s.sys.On("Stat", "/app/bin/srv").Return(fileInfo{dir: true}, nil)
				s.sys.On("LookPath", "srv").Return("/usr/bin/srv", nil)
			},
			want: "/usr/bin/srv",
		},
		{
			name: "no executable path uses PATH",
			setup: func() {
				s.sys.On("Executable").Return("", errors.New("no exe"))
				s.sys.On("LookPath", "srv").Return("/usr/bin/srv", nil)
			},
			want: "/usr/bin/srv",
		},
		{
			name: "missing everywhere",
			setup: func() {
				s.sys.On("Executable").Return("/app/bin/loop", nil)
				s.sys.On("Stat", "/app/bin/srv").Return(nil, os.ErrNotExist)
				s.sys.On("LookPath", "srv").Return("", errors.New("not found"))
			},
			err: true,
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			tc.setup()
			got, err := s.srv.FindBinary()
			if tc.err {
				require.ErrorIs(s.T(), err, ErrBinaryMissing)
				return
			}
			require.NoError(s.T(), err)
			require.Equal(s.T(), tc.want, got)
		})
	}
}

// The executable's symlinks are resolved, so a binary next to its target
// is found.
func (s *LocalServerSuite) TestFindBinaryResolvesSymlink() {
	dir := s.T().TempDir()
	real := filepath.Join(dir, "real")
	require.NoError(s.T(), os.Mkdir(real, 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(real, "loop"), nil, 0o755))
	link := filepath.Join(dir, "loop")
	require.NoError(s.T(), os.Symlink(filepath.Join(real, "loop"), link))
	realDir, err := filepath.EvalSymlinks(real)
	require.NoError(s.T(), err)
	s.sys.On("Executable").Return(link, nil)
	s.sys.On("Stat", filepath.Join(realDir, "srv")).Return(fileInfo{}, nil)

	got, err := s.srv.FindBinary()

	require.NoError(s.T(), err)
	require.Equal(s.T(), filepath.Join(realDir, "srv"), got)
}

// startable sets up a clean start: no stale pid file, port 4321, the
// process, and the health check passing after one miss.
func (s *LocalServerSuite) startable(proc *fakeProc) {
	s.bundled()
	s.sys.On("ReadFile", "/run/srv.pid").Return(nil, os.ErrNotExist).Once()
	s.sys.On("FreePort").Return(4321, nil).Once()
	s.sys.On("Start", "/app/bin/srv", []string{"--port", "4321"}, mock.Anything).Return(proc, nil).Once()
	s.http.On("Do", "http://127.0.0.1:4321/health").Return(nil, errors.New("refused")).Once()
	s.http.On("Do", "http://127.0.0.1:4321/health").Return(ok(), nil).Once()
	s.sys.On("WriteFile", "/run/srv.pid", "7 4321", os.FileMode(0o600)).Return(nil).Once()
}

func (s *LocalServerSuite) TestAcquireStartsOnceAndReuses() {
	proc := newFakeProc(7)
	s.startable(proc)

	url, err := s.srv.Acquire(context.Background())
	require.NoError(s.T(), err)
	require.Equal(s.T(), "http://127.0.0.1:4321", url)
	require.True(s.T(), s.srv.Running())

	url, err = s.srv.Acquire(context.Background())
	require.NoError(s.T(), err)
	require.Equal(s.T(), "http://127.0.0.1:4321", url)
	s.sys.AssertNumberOfCalls(s.T(), "Start", 1)

	s.srv.Release()
	s.srv.Release()
	s.srv.Release() // never goes negative
	require.Equal(s.T(), 0, s.srv.busy)
}

// A server that exited on its own is started again on the next Acquire.
func (s *LocalServerSuite) TestAcquireRestartsAfterExit() {
	first := newFakeProc(7)
	s.startable(first)
	_, err := s.srv.Acquire(context.Background())
	require.NoError(s.T(), err)
	s.srv.Release()
	first.exit()
	require.Eventually(s.T(), func() bool { return !s.srv.Running() }, time.Second, time.Millisecond)

	second := newFakeProc(7)
	s.startable(second)
	_, err = s.srv.Acquire(context.Background())
	require.NoError(s.T(), err)
	s.sys.AssertNumberOfCalls(s.T(), "Start", 2)
}

func (s *LocalServerSuite) TestAcquireErrors() {
	tests := []struct {
		name  string
		setup func()
		want  string
	}{
		{
			name: "binary missing",
			setup: func() {
				s.sys.On("Executable").Return("", errors.New("no exe"))
				s.sys.On("LookPath", "srv").Return("", errors.New("nope"))
			},
			want: "binary not found",
		},
		{
			name: "no free port",
			setup: func() {
				s.bundled()
				s.sys.On("ReadFile", "/run/srv.pid").Return(nil, os.ErrNotExist)
				s.sys.On("FreePort").Return(0, errors.New("exhausted"))
			},
			want: "free port: exhausted",
		},
		{
			name: "start fails",
			setup: func() {
				s.bundled()
				s.sys.On("ReadFile", "/run/srv.pid").Return(nil, os.ErrNotExist)
				s.sys.On("FreePort").Return(4321, nil)
				s.sys.On("Start", mock.Anything, mock.Anything, mock.Anything).Return(nil, errors.New("exec format error"))
			},
			want: "start srv: exec format error",
		},
		{
			name: "exits during startup, with its output",
			setup: func() {
				s.bundled()
				s.sys.On("ReadFile", "/run/srv.pid").Return(nil, os.ErrNotExist)
				s.sys.On("FreePort").Return(4321, nil)
				proc := newFakeProc(7)
				s.sys.On("Start", mock.Anything, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
					_, _ = args.Get(2).(io.Writer).Write([]byte("failed to load model\n"))
					proc.exit()
				}).Return(proc, nil)
				s.http.On("Do", mock.Anything).Return(status(http.StatusServiceUnavailable), nil)
			},
			want: "srv: exited during startup: failed to load model",
		},
		{
			name: "never answers",
			setup: func() {
				s.bundled()
				s.sys.On("ReadFile", "/run/srv.pid").Return(nil, os.ErrNotExist)
				s.sys.On("FreePort").Return(4321, nil)
				s.sys.On("Start", mock.Anything, mock.Anything, mock.Anything).Return(newFakeProc(7), nil)
				s.http.On("Do", mock.Anything).Return(nil, errors.New("refused"))
				s.srv.cfg.StartupTimeout = 20 * time.Millisecond
			},
			want: "context deadline exceeded",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			tc.setup()
			_, err := s.srv.Acquire(context.Background())
			require.ErrorContains(s.T(), err, tc.want)
			require.False(s.T(), s.srv.Running())
		})
	}
}

// A bad health path makes the request itself fail to build.
func (s *LocalServerSuite) TestHealthyBadPath() {
	s.srv.cfg.HealthPath = "/\x7f"
	require.False(s.T(), s.srv.healthy(context.Background(), 1))
}

func (s *LocalServerSuite) TestReapStale() {
	tests := []struct {
		name   string
		pid    string
		setup  func(stale *fakeProc)
		killed bool
	}{
		{name: "unparseable pid file", pid: "garbage"},
		{
			name: "process gone",
			pid:  "99 5555",
			setup: func(*fakeProc) {
				s.sys.On("FindProcess", 99).Return(nil, errors.New("no such process"))
			},
		},
		{
			name: "pid reused by something that isn't the server",
			pid:  "99 5555",
			setup: func(stale *fakeProc) {
				s.sys.On("FindProcess", 99).Return(stale, nil)
				s.http.On("Do", "http://127.0.0.1:5555/health").Return(nil, errors.New("refused"))
			},
		},
		{
			name: "server still answering is killed",
			pid:  "99 5555",
			setup: func(stale *fakeProc) {
				s.sys.On("FindProcess", 99).Return(stale, nil)
				s.http.On("Do", "http://127.0.0.1:5555/health").Return(ok(), nil)
			},
			killed: true,
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			stale := newFakeProc(99)
			s.sys.On("ReadFile", "/run/srv.pid").Return([]byte(tc.pid), nil)
			s.sys.On("Remove", "/run/srv.pid").Return(nil)
			if tc.setup != nil {
				tc.setup(stale)
			}
			s.srv.reapStale(context.Background())
			require.Equal(s.T(), tc.killed, stale.killed)
			s.sys.AssertCalled(s.T(), "Remove", "/run/srv.pid")
		})
	}
}

func (s *LocalServerSuite) TestStopIfIdle() {
	proc := newFakeProc(7)
	s.startable(proc)
	s.sys.On("Remove", "/run/srv.pid").Return(nil)

	require.False(s.T(), s.srv.StopIfIdle(), "not running")

	_, err := s.srv.Acquire(context.Background())
	require.NoError(s.T(), err)
	s.now = s.now.Add(time.Hour)
	require.False(s.T(), s.srv.StopIfIdle(), "in use")

	s.srv.Release()
	s.now = s.now.Add(30 * time.Second)
	require.False(s.T(), s.srv.StopIfIdle(), "recently used")

	s.now = s.now.Add(31 * time.Second)
	require.True(s.T(), s.srv.StopIfIdle())
	require.True(s.T(), proc.killed)
	require.False(s.T(), s.srv.Running())
}

func (s *LocalServerSuite) TestRunIdleMonitorStopsOnCancel() {
	proc := newFakeProc(7)
	s.startable(proc)
	s.sys.On("Remove", "/run/srv.pid").Return(nil)
	_, err := s.srv.Acquire(context.Background())
	require.NoError(s.T(), err)
	s.srv.Release()
	s.now = s.now.Add(2 * time.Minute)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.srv.RunIdleMonitor(ctx, time.Millisecond)
		close(done)
	}()
	require.Eventually(s.T(), func() bool { return !s.srv.Running() }, time.Second, time.Millisecond)
	cancel()
	<-done
	require.True(s.T(), proc.killed)
	s.srv.Stop() // already stopped: no-op
}

func (s *LocalServerSuite) TestTailBufferKeepsTheEnd() {
	b := &tailBuffer{max: 5}
	n, err := b.Write([]byte("abc"))
	require.NoError(s.T(), err)
	require.Equal(s.T(), 3, n)
	_, _ = b.Write([]byte("defg"))
	require.Equal(s.T(), "cdefg", b.String())
}

func (s *LocalServerSuite) TestRealSystem() {
	sys := RealSystem{}
	dir := s.T().TempDir()
	path := filepath.Join(dir, "f")

	require.NoError(s.T(), sys.WriteFile(path, []byte("x"), 0o600))
	data, err := sys.ReadFile(path)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "x", string(data))
	_, err = sys.Stat(path)
	require.NoError(s.T(), err)
	require.NoError(s.T(), sys.Remove(path))
	exe, err := sys.Executable()
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), exe)
	_, err = sys.LookPath("sh")
	require.NoError(s.T(), err)

	port, err := sys.FreePort()
	require.NoError(s.T(), err)
	require.Positive(s.T(), port)

	out := &tailBuffer{max: 100}
	proc, err := sys.Start("sh", []string{"-c", "echo hello; exec sleep 30"}, out)
	require.NoError(s.T(), err)
	require.Positive(s.T(), proc.Pid())

	require.Eventually(s.T(), func() bool { return strings.Contains(out.String(), "hello") }, 5*time.Second, 10*time.Millisecond)

	found, err := sys.FindProcess(proc.Pid())
	require.NoError(s.T(), err)
	require.Equal(s.T(), proc.Pid(), found.Pid())
	require.NoError(s.T(), found.Kill())
	require.Error(s.T(), proc.Wait())

	// osProcess.Wait on a process that isn't our child fails.
	require.Error(s.T(), found.Wait())
	_, err = sys.FindProcess(proc.Pid())
	require.Error(s.T(), err, "reaped process isn't running")

	_, err = sys.Start(filepath.Join(dir, "missing"), nil, out)
	require.Error(s.T(), err)

	child, err := sys.Start("sh", []string{"-c", "exec sleep 30"}, out)
	require.NoError(s.T(), err)
	require.NoError(s.T(), child.Kill())
	require.Error(s.T(), child.Wait())

	_, err = RealSystem{Listen: func(string, string) (net.Listener, error) { return nil, errors.New("denied") }}.FreePort()
	require.EqualError(s.T(), err, "denied")
}
