package pprofserver

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/config"
)

// logBuffer is a bytes.Buffer safe to log to from Run's goroutine while the
// test reads it.
type logBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *logBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *logBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type PprofServerSuite struct {
	suite.Suite
	cfg     *config.Config
	cfgErr  error
	listens []string
	listen  error
	ln      net.Listener
	logs    *logBuffer
	r       *Runner
}

func TestPprofServerSuite(t *testing.T) {
	suite.Run(t, new(PprofServerSuite))
}

func (s *PprofServerSuite) SetupTest() {
	s.cfg = &config.Config{}
	s.cfgErr = nil
	s.listens = nil
	s.listen = nil
	s.ln = nil
	s.logs = &logBuffer{}
	s.r = New(func() (*config.Config, error) { return s.cfg, s.cfgErr }, slog.New(slog.NewTextHandler(s.logs, nil)))
	// Every address listens on a free loopback port, so tests can ask for
	// any addr and reach it through s.ln.
	s.r.Listen = func(network, addr string) (net.Listener, error) {
		s.listens = append(s.listens, addr)
		if s.listen != nil {
			return nil, s.listen
		}
		ln, err := net.Listen(network, "127.0.0.1:0")
		s.ln = ln
		return ln, err
	}
}

func (s *PprofServerSuite) TearDownTest() {
	s.r.stop()
}

func (s *PprofServerSuite) enable(addr string) {
	s.cfg = &config.Config{Pprof: config.PprofConfig{Enabled: true, Addr: addr}}
}

// get fetches path from the listener s.ln, returning the status code or the
// dial error.
func (s *PprofServerSuite) get(path string) (int, error) {
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Get("http://" + s.ln.Addr().String() + path)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode, nil
}

func (s *PprofServerSuite) TestNew() {
	r := New(nil, slog.Default())
	require.NotNil(s.T(), r.Listen)
}

func (s *PprofServerSuite) TestSyncStartsServesAndStops() {
	s.enable("127.0.0.1:6060")
	s.r.sync()
	require.Equal(s.T(), []string{"127.0.0.1:6060"}, s.listens)
	code, err := s.get("/debug/pprof/")
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, code)
	require.Contains(s.T(), s.logs.String(), "pprof listening")

	// The same config again keeps the listener.
	s.r.sync()
	require.Len(s.T(), s.listens, 1)

	s.cfg = &config.Config{Pprof: config.PprofConfig{Addr: "127.0.0.1:6060"}}
	s.r.sync()
	_, err = s.get("/debug/pprof/")
	require.Error(s.T(), err)
	require.Contains(s.T(), s.logs.String(), "pprof stopped")
}

func (s *PprofServerSuite) TestSyncMovesToNewAddr() {
	s.enable("127.0.0.1:6060")
	s.r.sync()
	first := s.ln
	s.enable("127.0.0.1:6061")
	s.r.sync()
	require.Equal(s.T(), []string{"127.0.0.1:6060", "127.0.0.1:6061"}, s.listens)
	// The OS may hand the new listener the old one's port, so check the old
	// listener is closed rather than dialing its address.
	_, err := first.Accept()
	require.ErrorIs(s.T(), err, net.ErrClosed)
	code, err := s.get("/debug/pprof/cmdline")
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, code)
}

func (s *PprofServerSuite) TestSyncKeepsStateWhenConfigFails() {
	s.enable("127.0.0.1:6060")
	s.r.sync()
	s.cfgErr = errors.New("bad config")
	s.r.sync()
	code, err := s.get("/debug/pprof/")
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, code)
}

func (s *PprofServerSuite) TestSyncListenFailureLogsOnceAndRetries() {
	s.enable("127.0.0.1:6060")
	s.listen = errors.New("address in use")
	s.r.sync()
	s.r.sync()
	require.Len(s.T(), s.listens, 2)
	require.Equal(s.T(), 1, strings.Count(s.logs.String(), `"pprof listen"`))

	// Turning it off forgets the failure, so the next one logs again.
	s.cfg = &config.Config{}
	s.r.sync()
	s.enable("127.0.0.1:6060")
	s.r.sync()
	require.Equal(s.T(), 2, strings.Count(s.logs.String(), `"pprof listen"`))

	s.listen = nil
	s.r.sync()
	code, err := s.get("/debug/pprof/")
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, code)
}

func (s *PprofServerSuite) TestRunStopsOnCancel() {
	s.enable("127.0.0.1:6060")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.r.Run(ctx, time.Millisecond)
		close(done)
	}()
	require.Eventually(s.T(), func() bool { return strings.Contains(s.logs.String(), "pprof listening") }, 5*time.Second, time.Millisecond)
	cancel()
	<-done
	require.Len(s.T(), s.listens, 1)
	require.Contains(s.T(), s.logs.String(), "pprof stopped")
}

func (s *PprofServerSuite) TestHandler() {
	h := Handler()
	for _, path := range []string{"/debug/pprof/", "/debug/pprof/heap", "/debug/pprof/cmdline", "/debug/pprof/symbol"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		require.Equal(s.T(), http.StatusOK, rec.Code, path)
	}
	for _, path := range []string{"/debug/pprof/profile?seconds=1", "/debug/pprof/trace?seconds=0.01"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		require.Equal(s.T(), http.StatusOK, rec.Code, path)
	}
}
