package browser

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// mockCaptureClient implements CaptureClient for testing.
type mockCaptureClient struct {
	consoleCh  chan<- ConsoleMessage
	networkCh  chan<- NetworkRequest
	consoleErr error
	networkErr error
}

func (m *mockCaptureClient) EnableConsoleCapture(_ context.Context, ch chan<- ConsoleMessage) error {
	m.consoleCh = ch
	return m.consoleErr
}

func (m *mockCaptureClient) EnableNetworkCapture(_ context.Context, ch chan<- NetworkRequest) error {
	m.networkCh = ch
	return m.networkErr
}

type CaptureSuite struct {
	suite.Suite
}

func TestCaptureSuite(t *testing.T) {
	suite.Run(t, new(CaptureSuite))
}

func (s *CaptureSuite) TestEnable() {
	cs := &CaptureState{}
	client := &mockCaptureClient{}
	cs.Enable(context.Background(), client)

	require.True(s.T(), cs.Started)
	require.NotNil(s.T(), client.consoleCh)
	require.NotNil(s.T(), client.networkCh)

	// Send a console message and verify it's captured.
	client.consoleCh <- ConsoleMessage{Level: "log", Text: "hello", Time: time.Now()}
	time.Sleep(10 * time.Millisecond)

	result, err := cs.ReadConsole("", false, 100, false)
	require.NoError(s.T(), err)
	require.Contains(s.T(), result, "hello")
}

func (s *CaptureSuite) TestEnableNilClient() {
	cs := &CaptureState{}
	cs.Enable(context.Background(), nil)
	require.False(s.T(), cs.Started)
}

func (s *CaptureSuite) TestEnableAlreadyStarted() {
	cs := &CaptureState{Started: true}
	client := &mockCaptureClient{}
	cs.Enable(context.Background(), client)
	// Should not wire up — client channels remain nil.
	require.Nil(s.T(), client.consoleCh)
}

func (s *CaptureSuite) TestRewireAfterTabSwitch() {
	cs := &CaptureState{}

	// Initial client (tab 1).
	client1 := &mockCaptureClient{}
	cs.Enable(context.Background(), client1)

	client1.consoleCh <- ConsoleMessage{Level: "log", Text: "from tab 1", Time: time.Now()}
	client1.networkCh <- NetworkRequest{URL: "https://tab1.example.com", Method: "GET", Status: 200, Time: time.Now()}
	time.Sleep(10 * time.Millisecond)

	// Switch to tab 2 — rewire capture.
	client2 := &mockCaptureClient{}
	cs.Rewire(context.Background(), client2)

	require.NotNil(s.T(), client2.consoleCh)
	require.NotNil(s.T(), client2.networkCh)

	client2.consoleCh <- ConsoleMessage{Level: "error", Text: "from tab 2", Time: time.Now()}
	client2.networkCh <- NetworkRequest{URL: "https://tab2.example.com", Method: "POST", Status: 201, Time: time.Now()}
	time.Sleep(10 * time.Millisecond)

	// Both tabs' events should be in the buffer.
	consoleResult, err := cs.ReadConsole("", false, 100, false)
	require.NoError(s.T(), err)
	require.Contains(s.T(), consoleResult, "from tab 1")
	require.Contains(s.T(), consoleResult, "from tab 2")

	networkResult, err := cs.ReadNetwork("", 100, false)
	require.NoError(s.T(), err)
	require.Contains(s.T(), networkResult, "tab1.example.com")
	require.Contains(s.T(), networkResult, "tab2.example.com")
}

func (s *CaptureSuite) TestRewireNilClient() {
	cs := &CaptureState{Started: true}
	// Should not panic.
	cs.Rewire(context.Background(), nil)
}

func (s *CaptureSuite) TestRewireSameClientIsNoop() {
	cs := &CaptureState{}
	client := &mockCaptureClient{}
	cs.Enable(context.Background(), client)

	// Reset channels to detect if wireCapture is called again.
	client.consoleCh = nil
	client.networkCh = nil

	cs.Rewire(context.Background(), client)

	// Channels should still be nil — same client means no re-wiring.
	require.Nil(s.T(), client.consoleCh)
	require.Nil(s.T(), client.networkCh)
}

func (s *CaptureSuite) TestReadConsoleFiltering() {
	cs := &CaptureState{}
	client := &mockCaptureClient{}
	cs.Enable(context.Background(), client)

	now := time.Now()
	client.consoleCh <- ConsoleMessage{Level: "log", Text: "info msg", Time: now}
	client.consoleCh <- ConsoleMessage{Level: "error", Text: "error msg", Time: now}
	client.consoleCh <- ConsoleMessage{Level: "warning", Text: "warn msg", Time: now}
	time.Sleep(10 * time.Millisecond)

	// Only errors.
	result, err := cs.ReadConsole("", true, 100, false)
	require.NoError(s.T(), err)
	require.Contains(s.T(), result, "error msg")
	require.NotContains(s.T(), result, "info msg")

	// Pattern filter.
	result, err = cs.ReadConsole("warn", false, 100, false)
	require.NoError(s.T(), err)
	require.Contains(s.T(), result, "warn msg")
	require.NotContains(s.T(), result, "info msg")
}

func (s *CaptureSuite) TestReadConsoleInvalidPattern() {
	cs := &CaptureState{}
	_, err := cs.ReadConsole("[invalid", false, 100, false)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "invalid regex")
}

func (s *CaptureSuite) TestReadConsoleClear() {
	cs := &CaptureState{}
	client := &mockCaptureClient{}
	cs.Enable(context.Background(), client)

	client.consoleCh <- ConsoleMessage{Level: "log", Text: "msg", Time: time.Now()}
	time.Sleep(10 * time.Millisecond)

	result, err := cs.ReadConsole("", false, 100, true)
	require.NoError(s.T(), err)
	require.Contains(s.T(), result, "msg")

	// After clear, should be empty.
	result, err = cs.ReadConsole("", false, 100, false)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "No console messages", result)
}

func (s *CaptureSuite) TestReadConsoleLimit() {
	cs := &CaptureState{}
	client := &mockCaptureClient{}
	cs.Enable(context.Background(), client)

	for i := 0; i < 5; i++ {
		client.consoleCh <- ConsoleMessage{Level: "log", Text: "msg", Time: time.Now()}
	}
	time.Sleep(10 * time.Millisecond)

	result, err := cs.ReadConsole("", false, 2, false)
	require.NoError(s.T(), err)
	require.Contains(s.T(), result, "2 console message(s)")
}

func (s *CaptureSuite) TestReadNetworkFiltering() {
	cs := &CaptureState{}
	client := &mockCaptureClient{}
	cs.Enable(context.Background(), client)

	now := time.Now()
	client.networkCh <- NetworkRequest{URL: "https://api.example.com/users", Method: "GET", Status: 200, Time: now}
	client.networkCh <- NetworkRequest{URL: "https://cdn.example.com/style.css", Method: "GET", Status: 200, Time: now}
	time.Sleep(10 * time.Millisecond)

	result, err := cs.ReadNetwork("api\\.example", 100, false)
	require.NoError(s.T(), err)
	require.Contains(s.T(), result, "api.example.com")
	require.NotContains(s.T(), result, "cdn.example.com")
}

func (s *CaptureSuite) TestReadNetworkInvalidPattern() {
	cs := &CaptureState{}
	_, err := cs.ReadNetwork("[invalid", 100, false)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "invalid regex")
}

func (s *CaptureSuite) TestReadNetworkClear() {
	cs := &CaptureState{}
	client := &mockCaptureClient{}
	cs.Enable(context.Background(), client)

	client.networkCh <- NetworkRequest{URL: "https://example.com", Method: "GET", Status: 200, Time: time.Now()}
	time.Sleep(10 * time.Millisecond)

	result, err := cs.ReadNetwork("", 100, true)
	require.NoError(s.T(), err)
	require.Contains(s.T(), result, "example.com")

	result, err = cs.ReadNetwork("", 100, false)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "No network requests", result)
}

func (s *CaptureSuite) TestReadNetworkLimit() {
	cs := &CaptureState{}
	client := &mockCaptureClient{}
	cs.Enable(context.Background(), client)

	for i := 0; i < 5; i++ {
		client.networkCh <- NetworkRequest{URL: "https://example.com", Method: "GET", Status: 200, Time: time.Now()}
	}
	time.Sleep(10 * time.Millisecond)

	result, err := cs.ReadNetwork("", 2, false)
	require.NoError(s.T(), err)
	require.Contains(s.T(), result, "2 network request(s)")
}

func (s *CaptureSuite) TestReadConsoleDefaultLimit() {
	cs := &CaptureState{}
	cs.ConsoleMsgs = []ConsoleMessage{{Level: "log", Text: "msg", Time: time.Now()}}

	// limit=0 should default to 100 and still return the message.
	result, err := cs.ReadConsole("", false, 0, false)
	require.NoError(s.T(), err)
	require.Contains(s.T(), result, "1 console message(s)")
}

func (s *CaptureSuite) TestReadNetworkDefaultLimit() {
	cs := &CaptureState{}
	cs.NetworkReqs = []NetworkRequest{{URL: "https://example.com", Method: "GET", Status: 200, Time: time.Now()}}

	// limit=0 should default to 50 and still return the request.
	result, err := cs.ReadNetwork("", 0, false)
	require.NoError(s.T(), err)
	require.Contains(s.T(), result, "1 network request(s)")
}
