package api

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/dictation"
)

// mockDictation is a DictationService whose sessions transcribe with the
// TranscribeFunc Begin's expectation returns.
type mockDictation struct {
	mock.Mock
	released chan struct{}
}

func (m *mockDictation) Status() dictation.Status {
	return m.Called().Get(0).(dictation.Status)
}

func (m *mockDictation) DownloadModel(done chan<- struct{}) bool {
	return m.Called(done).Bool(0)
}

func (m *mockDictation) Begin(ctx context.Context, language string, emit func(dictation.Event)) (*dictation.Session, func(), error) {
	args := m.Called(ctx, language)
	if err := args.Error(1); err != nil {
		return nil, nil, err
	}
	transcribe := args.Get(0).(dictation.TranscribeFunc)
	return dictation.NewSession(transcribe, language, emit), func() {
		m.MethodCalled("release")
		close(m.released)
	}, nil
}

type DictationHandlerSuite struct {
	suite.Suite
	svc *mockDictation
	srv *Server
	mux *http.ServeMux
}

func TestDictationHandlerSuite(t *testing.T) { suite.Run(t, new(DictationHandlerSuite)) }

func (s *DictationHandlerSuite) SetupTest() {
	s.svc = &mockDictation{released: make(chan struct{})}
	s.srv = nilServer()
	s.srv.SetDictation(s.svc)
	s.mux = http.NewServeMux()
	s.srv.registerSystemRoutes(s.mux)
}

func (s *DictationHandlerSuite) TearDownTest() {
	s.svc.AssertExpectations(s.T())
}

func (s *DictationHandlerSuite) serve(method, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest(method, path, nil))
	return rec
}

func (s *DictationHandlerSuite) TestNotConfigured() {
	s.srv.SetDictation(nil)
	for _, route := range []string{"GET /api/dictation/status", "POST /api/dictation/model", "GET /api/dictation/stream"} {
		method, path, _ := strings.Cut(route, " ")
		require.Equal(s.T(), http.StatusNotImplemented, s.serve(method, path).Code, route)
	}
}

func (s *DictationHandlerSuite) TestStatus() {
	s.svc.On("Status").Return(dictation.Status{Available: true, Model: "m", ModelReady: true})

	rec := s.serve("GET", "/api/dictation/status")

	require.Equal(s.T(), http.StatusOK, rec.Code)
	var got dictation.Status
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(s.T(), dictation.Status{Available: true, Model: "m", ModelReady: true}, got)
}

func (s *DictationHandlerSuite) TestDownloadModel() {
	s.svc.On("DownloadModel", (chan<- struct{})(nil)).Return(true)
	s.svc.On("Status").Return(dictation.Status{Download: dictation.Progress{Active: true}})

	rec := s.serve("POST", "/api/dictation/model")

	require.Equal(s.T(), http.StatusAccepted, rec.Code)
	require.JSONEq(s.T(), `{"enabled":false,"available":false,"reason":"","model":"","model_ready":false,"model_size_bytes":0,"download":{"active":true,"error":"","received":0,"total":0},"running":false,"send_phrases":null}`, rec.Body.String())
}

func (s *DictationHandlerSuite) TestStreamNeedsWebSocket() {
	require.Equal(s.T(), http.StatusBadRequest, s.serve("GET", "/api/dictation/stream").Code)
}

// waitReleased waits for the stream to release its session.
func (s *DictationHandlerSuite) waitReleased() {
	select {
	case <-s.svc.released:
	case <-time.After(5 * time.Second):
		s.T().Fatal("session not released")
	}
}

// dial opens the stream on a test server.
func (s *DictationHandlerSuite) dial(query string) *websocket.Conn {
	ts := httptest.NewServer(s.mux)
	s.T().Cleanup(ts.Close)
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/api/dictation/stream"+query, nil)
	require.NoError(s.T(), err)
	s.T().Cleanup(func() { conn.Close() })
	return conn
}

// events reads the server's messages until it closes the stream normally.
func (s *DictationHandlerSuite) events(conn *websocket.Conn) []map[string]string {
	var out []map[string]string
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			require.True(s.T(), websocket.IsCloseError(err, websocket.CloseNormalClosure), err)
			return out
		}
		var e map[string]string
		require.NoError(s.T(), json.Unmarshal(data, &e))
		out = append(out, e)
	}
}

// speechPCM is n samples loud enough to be speech, as PCM16LE.
func speechPCM(n int) []byte {
	out := make([]byte, 2*n)
	for i := range n {
		v := int16(3000)
		if i%2 == 1 {
			v = -3000
		}
		binary.LittleEndian.PutUint16(out[2*i:], uint16(v))
	}
	return out
}

func (s *DictationHandlerSuite) TestStreamBeginFails() {
	tests := []struct {
		name string
		err  error
		code string
	}{
		{name: "dictation error", err: &dictation.Error{Code: dictation.CodeModelMissing, Err: errors.New("no model")}, code: dictation.CodeModelMissing},
		{name: "other error", err: errors.New("boom"), code: dictation.CodeEngineFailed},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.svc.On("Begin", mock.Anything, "").Return(nil, tc.err)

			got := s.events(s.dial(""))

			require.Equal(s.T(), []map[string]string{{"type": "error", "code": tc.code, "message": tc.err.Error()}}, got)
			s.svc.AssertExpectations(s.T())
		})
	}
}

func (s *DictationHandlerSuite) TestStreamStop() {
	var mu sync.Mutex
	var heard []int16
	transcribe := dictation.TranscribeFunc(func(_ context.Context, samples []int16, language, _ string) (dictation.Transcript, error) {
		mu.Lock()
		defer mu.Unlock()
		heard = samples
		return dictation.Transcript{Text: " hello " + language}, nil
	})
	s.svc.On("Begin", mock.Anything, "ro").Return(transcribe, nil)
	s.svc.On("release").Return()
	conn := s.dial("?language=ro")

	require.NoError(s.T(), conn.WriteMessage(websocket.BinaryMessage, speechPCM(1000)))
	require.NoError(s.T(), conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"nonsense"}`)))
	require.NoError(s.T(), conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"stop"}`)))

	require.Equal(s.T(), []map[string]string{{"type": "ready"}, {"type": "final", "text": "hello ro"}}, s.events(conn))
	mu.Lock()
	defer mu.Unlock()
	require.Len(s.T(), heard, 1000)
	require.Equal(s.T(), []int16{3000, -3000}, heard[:2])
	s.waitReleased()
}

func (s *DictationHandlerSuite) TestStreamEndsWithoutText() {
	tests := []struct {
		name string
		send func(conn *websocket.Conn) error
		want []map[string]string
	}{
		{
			name: "cancel",
			send: func(conn *websocket.Conn) error {
				return conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"cancel"}`))
			},
			want: []map[string]string{{"type": "ready"}},
		},
		{
			name: "odd audio",
			send: func(conn *websocket.Conn) error {
				return conn.WriteMessage(websocket.BinaryMessage, []byte{1, 2, 3})
			},
			want: []map[string]string{{"type": "ready"}, {"type": "error", "code": dictation.CodeBadAudio, "message": "audio must be 16-bit samples"}},
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.svc.On("Begin", mock.Anything, "").Return(dictation.TranscribeFunc(nil), nil)
			s.svc.On("release").Return()
			conn := s.dial("")

			require.NoError(s.T(), tc.send(conn))

			require.Equal(s.T(), tc.want, s.events(conn))
			s.waitReleased()
			s.svc.AssertExpectations(s.T())
		})
	}
}

// A client that goes away cancels its session.
func (s *DictationHandlerSuite) TestStreamClientGone() {
	s.svc.On("Begin", mock.Anything, "").Return(dictation.TranscribeFunc(nil), nil)
	s.svc.On("release").Return()
	conn := s.dial("")
	_, _, err := conn.ReadMessage()
	require.NoError(s.T(), err, "ready")

	require.NoError(s.T(), conn.Close())

	s.waitReleased()
}
