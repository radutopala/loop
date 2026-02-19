package embeddings

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// mockCommander mocks the Commander interface.
type mockCommander struct {
	mock.Mock
}

func (m *mockCommander) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	// Build a flat argument list for mock matching.
	callArgs := []any{ctx, name}
	for _, a := range args {
		callArgs = append(callArgs, a)
	}
	ret := m.Called(callArgs...)
	return ret.Get(0).([]byte), ret.Error(1)
}

// mockHTTPClient mocks the HTTPClient interface.
type mockHTTPClient struct {
	mock.Mock
}

func (m *mockHTTPClient) Do(req *http.Request) (*http.Response, error) {
	ret := m.Called(req)
	resp, _ := ret.Get(0).(*http.Response)
	return resp, ret.Error(1)
}

type OllamaSuite struct {
	suite.Suite
	cmd  *mockCommander
	http *mockHTTPClient
}

func TestOllamaSuite(t *testing.T) {
	suite.Run(t, new(OllamaSuite))
}

func (s *OllamaSuite) SetupTest() {
	s.cmd = new(mockCommander)
	s.http = new(mockHTTPClient)
}

func (s *OllamaSuite) newEmbedder() *OllamaEmbedder {
	return NewOllamaEmbedder(
		WithOllamaCommander(s.cmd),
		WithOllamaHTTPClient(s.http),
		WithOllamaURL("http://localhost:11434"),
		WithOllamaModel("nomic-embed-text"),
		WithOllamaStartupWait(10*time.Millisecond),
	)
}

func (s *OllamaSuite) TestDimensions() {
	e := s.newEmbedder()
	require.Equal(s.T(), 768, e.Dimensions())
}

func (s *OllamaSuite) TestEmbedContainerAlreadyRunning() {
	ctx := context.Background()
	e := s.newEmbedder()

	// Container already running.
	s.cmd.On("Run", ctx, "docker", "inspect", "--format", "{{.State.Running}}", "loop-ollama").
		Return([]byte("true\n"), nil)
	// Pull model.
	s.cmd.On("Run", ctx, "docker", "exec", "loop-ollama", "ollama", "list").
		Return([]byte(""), nil)
	s.cmd.On("Run", ctx, "docker", "exec", "loop-ollama", "ollama", "pull", "nomic-embed-text").
		Return([]byte(""), nil)

	// Embed HTTP call.
	s.http.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/api/embed"
	})).Return(&http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(`{"embeddings":[[0.1,0.2,0.3]]}`)),
	}, nil)

	result, err := e.Embed(ctx, []string{"hello"})
	require.NoError(s.T(), err)
	require.Len(s.T(), result, 1)
	require.Equal(s.T(), []float32{0.1, 0.2, 0.3}, result[0])
	s.cmd.AssertExpectations(s.T())
}

func (s *OllamaSuite) TestEmbedStartsContainer() {
	ctx := context.Background()
	e := s.newEmbedder()

	// Container not running.
	s.cmd.On("Run", ctx, "docker", "inspect", "--format", "{{.State.Running}}", "loop-ollama").
		Return([]byte(""), errors.New("not found"))
	// Remove stale.
	s.cmd.On("Run", ctx, "docker", "rm", "-f", "loop-ollama").
		Return([]byte(""), nil)
	// Docker run.
	s.cmd.On("Run", ctx, "docker", "run", "-d",
		"--name", "loop-ollama",
		"-v", "loop-ollama:/root/.ollama",
		"-p", "11434:11434",
		"ollama/ollama:latest",
	).Return([]byte("containerid\n"), nil)
	// Health check.
	s.http.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/"
	})).Return(&http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader("Ollama is running")),
	}, nil)
	// Pull model.
	s.cmd.On("Run", ctx, "docker", "exec", "loop-ollama", "ollama", "list").
		Return([]byte(""), nil)
	s.cmd.On("Run", ctx, "docker", "exec", "loop-ollama", "ollama", "pull", "nomic-embed-text").
		Return([]byte(""), nil)
	// Embed.
	s.http.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/api/embed"
	})).Return(&http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(`{"embeddings":[[1.0,2.0]]}`)),
	}, nil)

	result, err := e.Embed(ctx, []string{"test"})
	require.NoError(s.T(), err)
	require.Equal(s.T(), []float32{1.0, 2.0}, result[0])
	s.cmd.AssertExpectations(s.T())
}

func (s *OllamaSuite) TestEmbedDockerRunFails() {
	ctx := context.Background()
	e := s.newEmbedder()

	s.cmd.On("Run", ctx, "docker", "inspect", "--format", "{{.State.Running}}", "loop-ollama").
		Return([]byte(""), errors.New("not found"))
	s.cmd.On("Run", ctx, "docker", "rm", "-f", "loop-ollama").
		Return([]byte(""), nil)
	s.cmd.On("Run", ctx, "docker", "run", "-d",
		"--name", "loop-ollama",
		"-v", "loop-ollama:/root/.ollama",
		"-p", "11434:11434",
		"ollama/ollama:latest",
	).Return([]byte(""), errors.New("docker daemon not running"))

	_, err := e.Embed(ctx, []string{"test"})
	require.ErrorContains(s.T(), err, "docker run ollama")
}

func (s *OllamaSuite) TestEmbedModelAlreadyCached() {
	ctx := context.Background()
	e := s.newEmbedder()

	s.cmd.On("Run", ctx, "docker", "inspect", "--format", "{{.State.Running}}", "loop-ollama").
		Return([]byte("true\n"), nil)
	// ollama list returns the model — pull is skipped.
	s.cmd.On("Run", ctx, "docker", "exec", "loop-ollama", "ollama", "list").
		Return([]byte("NAME            ID\nnomic-embed-text:latest    abc123\n"), nil)

	s.http.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/api/embed"
	})).Return(&http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(`{"embeddings":[[0.5]]}`)),
	}, nil)

	result, err := e.Embed(ctx, []string{"test"})
	require.NoError(s.T(), err)
	require.Equal(s.T(), []float32{0.5}, result[0])
	// Verify pull was never called.
	s.cmd.AssertNotCalled(s.T(), "Run", ctx, "docker", "exec", "loop-ollama", "ollama", "pull", "nomic-embed-text")
}

func (s *OllamaSuite) TestEmbedModelPullFails() {
	ctx := context.Background()
	e := s.newEmbedder()

	s.cmd.On("Run", ctx, "docker", "inspect", "--format", "{{.State.Running}}", "loop-ollama").
		Return([]byte("true\n"), nil)
	s.cmd.On("Run", ctx, "docker", "exec", "loop-ollama", "ollama", "list").
		Return([]byte(""), nil)
	s.cmd.On("Run", ctx, "docker", "exec", "loop-ollama", "ollama", "pull", "nomic-embed-text").
		Return([]byte(""), errors.New("pull failed"))

	_, err := e.Embed(ctx, []string{"test"})
	require.ErrorContains(s.T(), err, "ollama pull nomic-embed-text")
}

func (s *OllamaSuite) TestEmbedHTTPError() {
	ctx := context.Background()
	e := s.newEmbedder()

	s.cmd.On("Run", ctx, "docker", "inspect", "--format", "{{.State.Running}}", "loop-ollama").
		Return([]byte("true\n"), nil)
	s.cmd.On("Run", ctx, "docker", "exec", "loop-ollama", "ollama", "list").
		Return([]byte(""), nil)
	s.cmd.On("Run", ctx, "docker", "exec", "loop-ollama", "ollama", "pull", "nomic-embed-text").
		Return([]byte(""), nil)
	s.http.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/api/embed"
	})).Return((*http.Response)(nil), errors.New("connection refused"))

	_, err := e.Embed(ctx, []string{"test"})
	require.ErrorContains(s.T(), err, "ollama embed request")
}

func (s *OllamaSuite) TestEmbedNon200() {
	ctx := context.Background()
	e := s.newEmbedder()

	s.cmd.On("Run", ctx, "docker", "inspect", "--format", "{{.State.Running}}", "loop-ollama").
		Return([]byte("true\n"), nil)
	s.cmd.On("Run", ctx, "docker", "exec", "loop-ollama", "ollama", "list").
		Return([]byte(""), nil)
	s.cmd.On("Run", ctx, "docker", "exec", "loop-ollama", "ollama", "pull", "nomic-embed-text").
		Return([]byte(""), nil)
	s.http.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/api/embed"
	})).Return(&http.Response{
		StatusCode: 500,
		Body:       io.NopCloser(strings.NewReader("internal error")),
	}, nil)

	_, err := e.Embed(ctx, []string{"test"})
	require.ErrorContains(s.T(), err, "status 500")
}

func (s *OllamaSuite) TestEmbedCountMismatch() {
	ctx := context.Background()
	e := s.newEmbedder()

	s.cmd.On("Run", ctx, "docker", "inspect", "--format", "{{.State.Running}}", "loop-ollama").
		Return([]byte("true\n"), nil)
	s.cmd.On("Run", ctx, "docker", "exec", "loop-ollama", "ollama", "list").
		Return([]byte(""), nil)
	s.cmd.On("Run", ctx, "docker", "exec", "loop-ollama", "ollama", "pull", "nomic-embed-text").
		Return([]byte(""), nil)
	s.http.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/api/embed"
	})).Return(&http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(`{"embeddings":[[1.0]]}`)),
	}, nil)

	_, err := e.Embed(ctx, []string{"a", "b"})
	require.ErrorContains(s.T(), err, "expected 2 embeddings, got 1")
}

func (s *OllamaSuite) TestEmbedInvalidJSON() {
	ctx := context.Background()
	e := s.newEmbedder()

	s.cmd.On("Run", ctx, "docker", "inspect", "--format", "{{.State.Running}}", "loop-ollama").
		Return([]byte("true\n"), nil)
	s.cmd.On("Run", ctx, "docker", "exec", "loop-ollama", "ollama", "list").
		Return([]byte(""), nil)
	s.cmd.On("Run", ctx, "docker", "exec", "loop-ollama", "ollama", "pull", "nomic-embed-text").
		Return([]byte(""), nil)
	s.http.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/api/embed"
	})).Return(&http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(`not json`)),
	}, nil)

	_, err := e.Embed(ctx, []string{"test"})
	require.ErrorContains(s.T(), err, "ollama decode response")
}

func (s *OllamaSuite) TestWaitForReadyTimeout() {
	ctx := context.Background()
	origSleep := sleepFunc
	sleepFunc = func(_ time.Duration) {} // no-op to avoid slow tests
	defer func() { sleepFunc = origSleep }()

	e := s.newEmbedder()

	s.cmd.On("Run", ctx, "docker", "inspect", "--format", "{{.State.Running}}", "loop-ollama").
		Return([]byte(""), errors.New("not found"))
	s.cmd.On("Run", ctx, "docker", "rm", "-f", "loop-ollama").
		Return([]byte(""), nil)
	s.cmd.On("Run", ctx, "docker", "run", "-d",
		"--name", "loop-ollama",
		"-v", "loop-ollama:/root/.ollama",
		"-p", "11434:11434",
		"ollama/ollama:latest",
	).Return([]byte("cid\n"), nil)
	// Health check always fails.
	s.http.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/"
	})).Return((*http.Response)(nil), errors.New("refused"))

	_, err := e.Embed(ctx, []string{"test"})
	require.ErrorContains(s.T(), err, "ollama did not become ready")
}

func (s *OllamaSuite) TestStopRemovesContainer() {
	e := s.newEmbedder()

	s.cmd.On("Run", mock.Anything, "docker", "rm", "-f", "loop-ollama").
		Return([]byte(""), nil)
	e.Stop()
	s.cmd.AssertExpectations(s.T())
}

func (s *OllamaSuite) TestEmbedInvalidURL() {
	ctx := context.Background()
	e := NewOllamaEmbedder(
		WithOllamaCommander(s.cmd),
		WithOllamaHTTPClient(s.http),
		WithOllamaURL("http://localhost\x00:11434"),
		WithOllamaModel("nomic-embed-text"),
	)

	s.cmd.On("Run", ctx, "docker", "inspect", "--format", "{{.State.Running}}", "loop-ollama").
		Return([]byte("true\n"), nil)
	s.cmd.On("Run", ctx, "docker", "exec", "loop-ollama", "ollama", "list").
		Return([]byte(""), nil)
	s.cmd.On("Run", ctx, "docker", "exec", "loop-ollama", "ollama", "pull", "nomic-embed-text").
		Return([]byte(""), nil)

	_, err := e.Embed(ctx, []string{"test"})
	require.ErrorContains(s.T(), err, "ollama create request")
}

func (s *OllamaSuite) TestWaitForReadyInvalidURL() {
	ctx := context.Background()
	origSleep := sleepFunc
	sleepFunc = func(_ time.Duration) {}
	defer func() { sleepFunc = origSleep }()

	e := NewOllamaEmbedder(
		WithOllamaCommander(s.cmd),
		WithOllamaHTTPClient(s.http),
		WithOllamaURL("http://localhost\x00:11434"),
		WithOllamaModel("nomic-embed-text"),
		WithOllamaStartupWait(10*time.Millisecond),
	)

	s.cmd.On("Run", ctx, "docker", "inspect", "--format", "{{.State.Running}}", "loop-ollama").
		Return([]byte(""), errors.New("not found"))
	s.cmd.On("Run", ctx, "docker", "rm", "-f", "loop-ollama").
		Return([]byte(""), nil)
	s.cmd.On("Run", ctx, "docker", "run", "-d",
		"--name", "loop-ollama",
		"-v", "loop-ollama:/root/.ollama",
		"-p", "11434:11434",
		"ollama/ollama:latest",
	).Return([]byte("cid\n"), nil)

	_, err := e.Embed(ctx, []string{"test"})
	require.ErrorContains(s.T(), err, "ollama ensure running")
}

func (s *OllamaSuite) TestNewOllamaEmbedderDefaults() {
	e := NewOllamaEmbedder()
	require.Equal(s.T(), "nomic-embed-text", e.model)
	require.Equal(s.T(), "http://localhost:11434", e.url)
	require.Equal(s.T(), 768, e.dims)
	require.NotNil(s.T(), e.httpClient)
	require.NotNil(s.T(), e.commander)
	require.NotNil(s.T(), e.logger)
	require.Equal(s.T(), 30*time.Second, e.startupWait)
	require.Equal(s.T(), 1*time.Minute, e.idleCheckInterval)
}

func (s *OllamaSuite) TestWithOllamaOptions() {
	e := NewOllamaEmbedder(
		WithOllamaURL("http://custom:1234"),
		WithOllamaModel("custom-model"),
		WithOllamaLogger(nil),
		WithOllamaStartupWait(5*time.Second),
		WithOllamaLoopDir("/tmp/loop"),
		WithOllamaIdleCheckInterval(30*time.Second),
	)
	require.Equal(s.T(), "http://custom:1234", e.url)
	require.Equal(s.T(), "custom-model", e.model)
	require.Equal(s.T(), 5*time.Second, e.startupWait)
	require.Equal(s.T(), "/tmp/loop", e.loopDir)
	require.Equal(s.T(), 30*time.Second, e.idleCheckInterval)
}

// --- touchMarkerFile tests ---

func (s *OllamaSuite) TestTouchMarkerFile() {
	tmpDir := s.T().TempDir()
	e := NewOllamaEmbedder(
		WithOllamaCommander(s.cmd),
		WithOllamaLoopDir(tmpDir),
	)
	e.touchMarkerFile()

	_, err := os.Stat(filepath.Join(tmpDir, "ollama-last-used"))
	require.NoError(s.T(), err)
}

func (s *OllamaSuite) TestTouchMarkerFileNoLoopDir() {
	e := NewOllamaEmbedder(
		WithOllamaCommander(s.cmd),
	)
	// Should be a no-op — no panic, no file created.
	e.touchMarkerFile()
}

func (s *OllamaSuite) TestEmbedTouchesMarkerFile() {
	ctx := context.Background()
	tmpDir := s.T().TempDir()
	e := NewOllamaEmbedder(
		WithOllamaCommander(s.cmd),
		WithOllamaHTTPClient(s.http),
		WithOllamaURL("http://localhost:11434"),
		WithOllamaModel("nomic-embed-text"),
		WithOllamaStartupWait(10*time.Millisecond),
		WithOllamaLoopDir(tmpDir),
	)

	s.cmd.On("Run", ctx, "docker", "inspect", "--format", "{{.State.Running}}", "loop-ollama").
		Return([]byte("true\n"), nil)
	s.cmd.On("Run", ctx, "docker", "exec", "loop-ollama", "ollama", "list").
		Return([]byte("nomic-embed-text:latest abc123\n"), nil)
	s.http.On("Do", mock.MatchedBy(func(r *http.Request) bool {
		return r.URL.Path == "/api/embed"
	})).Return(&http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader(`{"embeddings":[[0.1]]}`)),
	}, nil)

	_, err := e.Embed(ctx, []string{"test"})
	require.NoError(s.T(), err)

	// Verify marker file was created.
	_, err = os.Stat(filepath.Join(tmpDir, "ollama-last-used"))
	require.NoError(s.T(), err)
}

// --- checkIdle tests ---

func (s *OllamaSuite) TestCheckIdleStopsContainer() {
	tmpDir := s.T().TempDir()
	markerPath := filepath.Join(tmpDir, "ollama-last-used")
	require.NoError(s.T(), os.WriteFile(markerPath, nil, 0o644))
	// Set mtime to 10 minutes ago.
	past := time.Now().Add(-10 * time.Minute)
	require.NoError(s.T(), os.Chtimes(markerPath, past, past))

	e := NewOllamaEmbedder(
		WithOllamaCommander(s.cmd),
		WithOllamaLoopDir(tmpDir),
	)

	s.cmd.On("Run", mock.Anything, "docker", "inspect", "--format", "{{.State.Running}}", "loop-ollama").
		Return([]byte("true\n"), nil)
	s.cmd.On("Run", mock.Anything, "docker", "rm", "-f", "loop-ollama").
		Return([]byte(""), nil)

	e.checkIdle()
	s.cmd.AssertCalled(s.T(), "Run", mock.Anything, "docker", "rm", "-f", "loop-ollama")
}

func (s *OllamaSuite) TestCheckIdleRecentMarker() {
	tmpDir := s.T().TempDir()
	markerPath := filepath.Join(tmpDir, "ollama-last-used")
	require.NoError(s.T(), os.WriteFile(markerPath, nil, 0o644))
	// mtime is now — recently used.

	e := NewOllamaEmbedder(
		WithOllamaCommander(s.cmd),
		WithOllamaLoopDir(tmpDir),
	)

	e.checkIdle()
	// Should not try to inspect or remove container.
	s.cmd.AssertNotCalled(s.T(), "Run", mock.Anything, "docker", "inspect", mock.Anything, mock.Anything, mock.Anything)
}

func (s *OllamaSuite) TestCheckIdleNoMarkerFile() {
	tmpDir := s.T().TempDir()
	e := NewOllamaEmbedder(
		WithOllamaCommander(s.cmd),
		WithOllamaLoopDir(tmpDir),
	)

	e.checkIdle()
	// No marker file — should not call any docker commands.
	s.cmd.AssertNotCalled(s.T(), "Run", mock.Anything, mock.Anything, mock.Anything)
}

func (s *OllamaSuite) TestCheckIdleContainerNotRunning() {
	tmpDir := s.T().TempDir()
	markerPath := filepath.Join(tmpDir, "ollama-last-used")
	require.NoError(s.T(), os.WriteFile(markerPath, nil, 0o644))
	past := time.Now().Add(-10 * time.Minute)
	require.NoError(s.T(), os.Chtimes(markerPath, past, past))

	e := NewOllamaEmbedder(
		WithOllamaCommander(s.cmd),
		WithOllamaLoopDir(tmpDir),
	)

	// Container not running.
	s.cmd.On("Run", mock.Anything, "docker", "inspect", "--format", "{{.State.Running}}", "loop-ollama").
		Return([]byte(""), errors.New("not found"))

	e.checkIdle()
	// Should not try to remove.
	s.cmd.AssertNotCalled(s.T(), "Run", mock.Anything, "docker", "rm", "-f", "loop-ollama")
}

// --- RunIdleMonitor tests ---

func (s *OllamaSuite) TestRunIdleMonitorExitsOnCancel() {
	e := NewOllamaEmbedder(
		WithOllamaCommander(s.cmd),
		WithOllamaLoopDir(s.T().TempDir()),
		WithOllamaIdleCheckInterval(time.Hour), // long interval, won't tick
	)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		e.RunIdleMonitor(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
		// success
	case <-time.After(time.Second):
		s.T().Fatal("RunIdleMonitor did not exit")
	}
}

func (s *OllamaSuite) TestRunIdleMonitorNoLoopDir() {
	e := NewOllamaEmbedder(
		WithOllamaCommander(s.cmd),
	)
	// Should return immediately since loopDir is empty.
	done := make(chan struct{})
	go func() {
		e.RunIdleMonitor(context.Background())
		close(done)
	}()
	select {
	case <-done:
		// success
	case <-time.After(time.Second):
		s.T().Fatal("RunIdleMonitor did not exit")
	}
}

func (s *OllamaSuite) TestRunIdleMonitorChecksIdle() {
	tmpDir := s.T().TempDir()
	markerPath := filepath.Join(tmpDir, "ollama-last-used")
	require.NoError(s.T(), os.WriteFile(markerPath, nil, 0o644))
	past := time.Now().Add(-10 * time.Minute)
	require.NoError(s.T(), os.Chtimes(markerPath, past, past))

	e := NewOllamaEmbedder(
		WithOllamaCommander(s.cmd),
		WithOllamaLoopDir(tmpDir),
		WithOllamaIdleCheckInterval(10*time.Millisecond),
	)

	s.cmd.On("Run", mock.Anything, "docker", "inspect", "--format", "{{.State.Running}}", "loop-ollama").
		Return([]byte("true\n"), nil)
	s.cmd.On("Run", mock.Anything, "docker", "rm", "-f", "loop-ollama").
		Return([]byte(""), nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		e.RunIdleMonitor(ctx)
		close(done)
	}()

	// Wait for at least one tick to fire.
	time.Sleep(50 * time.Millisecond)
	cancel()
	<-done

	s.cmd.AssertCalled(s.T(), "Run", mock.Anything, "docker", "rm", "-f", "loop-ollama")
}

func (s *OllamaSuite) TestExecCommander() {
	c := &ExecCommander{}
	// Run a simple command that should succeed.
	out, err := c.Run(context.Background(), "echo", "hello")
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(out), "hello")
}
