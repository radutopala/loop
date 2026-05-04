package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/browser"
)

// --- Start success ---

func (s *BrowserHandlerSuite) TestStartSuccess() {
	ws, ts, _ := s.startBrowserWS()
	defer ts.Close()
	defer ws.Close()
}

func (s *BrowserHandlerSuite) TestStartCDPConnectError() {
	s.browserMgr.On("EnsureBrowser", mock.Anything, "ch-1", "").Return(nil)
	s.browserMgr.On("GetCDPEndpoint", "ch-1").Return("ws://127.0.0.1:9222")

	// Create a CDPManager with a factory that always fails.
	cdpMgr := browser.NewCDPManager("ws://test:9222", browser.CDPManagerConfig{
		MaxRetries: 1,
		RetryDelay: time.Millisecond,
	}, slog.Default())
	browser.SetCDPFactoryForTest(cdpMgr, func(_ context.Context, _ string, _ *slog.Logger, _ ...browser.CDPOption) (browser.CDPSession, error) {
		return nil, errors.New("cdp connect failed")
	})
	s.srv.cdpManagersMu.Lock()
	if s.srv.cdpManagers == nil {
		s.srv.cdpManagers = make(map[string]*browser.CDPManager)
	}
	s.srv.cdpManagers["ch-1|docker"] = cdpMgr
	s.srv.cdpManagersMu.Unlock()

	ws, ts := s.dialBrowserWS()
	defer ts.Close()
	defer ws.Close()

	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgStart, ChannelID: "ch-1"}))
	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespError, resp.Type)
	require.Contains(s.T(), resp.Message, "failed to connect CDP")
}

// --- Screencast success ---

func (s *BrowserHandlerSuite) TestScreencastSendsBinaryFrames() {
	ws, ts, mockCDP := s.startBrowserWS()
	defer ts.Close()
	defer ws.Close()

	frameCh := make(chan []byte, 2)
	mockCDP.On("StartScreencast", 60, 1280, 900).Return((<-chan []byte)(frameCh))

	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgScreencast}))

	frame1 := []byte{0xFF, 0xD8, 0x01, 0x02, 0x03}
	frame2 := []byte{0xFF, 0xD8, 0x04, 0x05, 0x06}
	frameCh <- frame1
	frameCh <- frame2

	require.NoError(s.T(), ws.SetReadDeadline(time.Now().Add(2*time.Second)))
	msgType, data, err := ws.ReadMessage()
	require.NoError(s.T(), err)
	require.Equal(s.T(), websocket.BinaryMessage, msgType)
	require.Equal(s.T(), frame1, data)

	msgType, data, err = ws.ReadMessage()
	require.NoError(s.T(), err)
	require.Equal(s.T(), websocket.BinaryMessage, msgType)
	require.Equal(s.T(), frame2, data)

	close(frameCh)
	time.Sleep(20 * time.Millisecond)
}

func (s *BrowserHandlerSuite) TestWsFrameSenderStopCh() {
	stopCh := make(chan struct{})
	bc := &browserWSConn{stopCh: stopCh}
	sender := &wsFrameSender{bc: bc, stopCh: stopCh}
	ch := sender.StopCh()
	require.NotNil(s.T(), ch)
	close(stopCh)
	<-ch // should not block
}

// --- dispatchInput all types ---

func (s *BrowserHandlerSuite) TestInputClick() {
	ws, ts, mockCDP := s.startBrowserWS()
	defer ts.Close()
	defer ws.Close()

	mockCDP.On("MouseClick", mock.Anything, float64(100), float64(200), "left", 1).Return(nil)
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgInput, InputType: "click", X: 100, Y: 200}))
	time.Sleep(20 * time.Millisecond)
	mockCDP.AssertCalled(s.T(), "MouseClick", mock.Anything, float64(100), float64(200), "left", 1)
}

func (s *BrowserHandlerSuite) TestInputClickWithButtonAndCount() {
	ws, ts, mockCDP := s.startBrowserWS()
	defer ts.Close()
	defer ws.Close()

	mockCDP.On("MouseClick", mock.Anything, float64(10), float64(20), "right", 2).Return(nil)
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgInput, InputType: "click", X: 10, Y: 20, Button: "right", ClickCount: 2}))
	time.Sleep(20 * time.Millisecond)
	mockCDP.AssertCalled(s.T(), "MouseClick", mock.Anything, float64(10), float64(20), "right", 2)
}

func (s *BrowserHandlerSuite) TestInputMouseMove() {
	ws, ts, mockCDP := s.startBrowserWS()
	defer ts.Close()
	defer ws.Close()

	mockCDP.On("MouseMove", mock.Anything, float64(50), float64(60), 0).Return(nil)
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgInput, InputType: "mousemove", X: 50, Y: 60}))
	time.Sleep(20 * time.Millisecond)
	mockCDP.AssertCalled(s.T(), "MouseMove", mock.Anything, float64(50), float64(60), 0)
}

func (s *BrowserHandlerSuite) TestInputScroll() {
	ws, ts, mockCDP := s.startBrowserWS()
	defer ts.Close()
	defer ws.Close()

	mockCDP.On("MouseScroll", mock.Anything, float64(0), float64(0), float64(0), float64(-3)).Return(nil)
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgInput, InputType: "scroll", DeltaY: -3}))
	time.Sleep(20 * time.Millisecond)
	mockCDP.AssertCalled(s.T(), "MouseScroll", mock.Anything, float64(0), float64(0), float64(0), float64(-3))
}

func (s *BrowserHandlerSuite) TestInputKeyPress() {
	ws, ts, mockCDP := s.startBrowserWS()
	defer ts.Close()
	defer ws.Close()

	mockCDP.On("KeyPress", mock.Anything, "Enter").Return(nil)
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgInput, InputType: "keypress", Key: "Enter"}))
	time.Sleep(20 * time.Millisecond)
	mockCDP.AssertCalled(s.T(), "KeyPress", mock.Anything, "Enter")
}

func (s *BrowserHandlerSuite) TestInputTypeText() {
	ws, ts, mockCDP := s.startBrowserWS()
	defer ts.Close()
	defer ws.Close()

	mockCDP.On("TypeText", mock.Anything, "hello").Return(nil)
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgInput, InputType: "typetext", Text: "hello"}))
	time.Sleep(20 * time.Millisecond)
	mockCDP.AssertCalled(s.T(), "TypeText", mock.Anything, "hello")
}

func (s *BrowserHandlerSuite) TestInputDispatchError() {
	ws, ts, mockCDP := s.startBrowserWS()
	defer ts.Close()
	defer ws.Close()

	mockCDP.On("MouseClick", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("click fail"))
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgInput, InputType: "click", X: 1, Y: 2}))
	time.Sleep(20 * time.Millisecond)
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: "unknown"}))
	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespError, resp.Type)
}

// --- pipeFrames ---

type mockFrameSender struct {
	sendErr error
	stopCh  chan struct{}
}

func (m *mockFrameSender) SendFrame([]byte) error  { return m.sendErr }
func (m *mockFrameSender) StopCh() <-chan struct{} { return m.stopCh }

func (s *BrowserHandlerSuite) TestPipeFramesStopCh() {
	bc := &browserWSConn{
		logger: slog.Default(),
		stopCh: make(chan struct{}),
	}

	ms := &mockFrameSender{stopCh: make(chan struct{})}
	frameCh := make(chan []byte, 1)

	done := make(chan struct{})
	go func() {
		bc.pipeFrames(frameCh, ms)
		close(done)
	}()

	close(bc.stopCh)
	select {
	case <-done:
	case <-time.After(time.Second):
		s.T().Fatal("pipeFrames did not exit on stopCh close")
	}
}

func (s *BrowserHandlerSuite) TestPipeFramesChannelClosed() {
	bc := &browserWSConn{
		logger: slog.Default(),
		stopCh: make(chan struct{}),
	}

	ms := &mockFrameSender{stopCh: make(chan struct{})}
	frameCh := make(chan []byte, 1)

	done := make(chan struct{})
	go func() {
		bc.pipeFrames(frameCh, ms)
		close(done)
	}()

	close(frameCh)
	select {
	case <-done:
	case <-time.After(time.Second):
		s.T().Fatal("pipeFrames did not exit on frameCh close")
	}
}

func (s *BrowserHandlerSuite) TestPipeFramesSendFrameError() {
	bc := &browserWSConn{
		logger: slog.Default(),
		stopCh: make(chan struct{}),
	}

	ms := &mockFrameSender{
		sendErr: errors.New("send failed"),
		stopCh:  make(chan struct{}),
	}

	frameCh := make(chan []byte, 1)
	frameCh <- []byte("frame-data")

	done := make(chan struct{})
	go func() {
		bc.pipeFrames(frameCh, ms)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		s.T().Fatal("pipeFrames did not exit on SendFrame error")
	}
}

func (s *BrowserHandlerSuite) TestPipeFramesSendFrameSuccess() {
	bc := &browserWSConn{
		logger: slog.Default(),
		stopCh: make(chan struct{}),
	}

	ms := &mockFrameSender{stopCh: make(chan struct{})}

	frameCh := make(chan []byte, 1)
	frameCh <- []byte("frame-data")

	done := make(chan struct{})
	go func() {
		bc.pipeFrames(frameCh, ms)
		close(done)
	}()

	time.Sleep(10 * time.Millisecond)
	close(bc.stopCh)

	select {
	case <-done:
	case <-time.After(time.Second):
		s.T().Fatal("pipeFrames did not exit")
	}
}

func (s *BrowserHandlerSuite) TestPipeFramesStreamStopCh() {
	bc := &browserWSConn{
		logger: slog.Default(),
		stopCh: make(chan struct{}),
	}

	ms := &mockFrameSender{stopCh: make(chan struct{})}
	frameCh := make(chan []byte, 1)

	done := make(chan struct{})
	go func() {
		bc.pipeFrames(frameCh, ms)
		close(done)
	}()

	close(ms.stopCh)
	select {
	case <-done:
	case <-time.After(time.Second):
		s.T().Fatal("pipeFrames did not exit on stream StopCh close")
	}
}

// --- cleanup ---

func (s *BrowserHandlerSuite) TestCleanupWithStream() {
	ws, ts, _ := s.startBrowserWS()
	defer ts.Close()
	ws.Close()
	time.Sleep(50 * time.Millisecond)
}

// --- WS upgrade error ---

func (s *BrowserHandlerSuite) TestBrowserWSUpgradeError() {
	srv := nilServer()
	mgr := new(mockBrowserProvider)
	srv.SetBrowserProvider(mgr)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ws/browser", srv.handleBrowserWS)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/ws/browser")
	require.NoError(s.T(), err)
	resp.Body.Close()
}

func (s *BrowserHandlerSuite) TestSendJSONWriteError() {
	ws, ts, _ := s.startBrowserWS()
	defer ts.Close()

	ws.Close()
	time.Sleep(20 * time.Millisecond)

	connReady := make(chan *websocket.Conn, 1)
	tsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		connReady <- conn
	}))
	defer tsSrv.Close()

	wsURL := "ws" + strings.TrimPrefix(tsSrv.URL, "http") + "/"
	clientWS, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(s.T(), err)
	defer clientWS.Close()

	serverConn := <-connReady
	serverConn.Close()

	bc := &browserWSConn{
		conn:   serverConn,
		logger: slog.Default(),
		stopCh: make(chan struct{}),
	}
	bc.sendJSON(browserWSResponse{Type: bwsRespError, Message: "test"})
}
