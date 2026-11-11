package dictation

import (
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const (
	testWait = 5 * time.Second
	testTick = 5 * time.Millisecond
)

type EngineSuite struct {
	suite.Suite
}

func TestEngineSuite(t *testing.T) { suite.Run(t, new(EngineSuite)) }

func (s *EngineSuite) TestServerArgs() {
	require.Equal(s.T(),
		[]string{"-m", "/m.bin", "--host", "127.0.0.1", "--port", "9", "-bs", "1", "-bo", "1", "-nt", "-sns"},
		ServerArgs("/m.bin", 0, 9))
	require.Equal(s.T(),
		[]string{"-m", "/m.bin", "--host", "127.0.0.1", "--port", "9", "-bs", "1", "-bo", "1", "-nt", "-sns", "-t", "6"},
		ServerArgs("/m.bin", 6, 9))
}

func (s *EngineSuite) TestAudioCtx() {
	tests := []struct {
		secs float64
		want int
	}{
		{secs: 2, want: 512},
		{secs: 7.3, want: 512},
		{secs: 7.4, want: 768},
		{secs: 10, want: 768},
		{secs: 14, want: 1024},
		{secs: 15, want: 1500},
		{secs: 30, want: 1500},
	}
	for _, tc := range tests {
		require.Equal(s.T(), tc.want, audioCtx(int(tc.secs*SampleRate)), tc.secs)
	}
}

func (s *EngineSuite) TestEncodeWAV() {
	wav := EncodeWAV([]int16{1, -2})
	require.Len(s.T(), wav, 48)
	require.Equal(s.T(), "RIFF", string(wav[:4]))
	require.Equal(s.T(), uint32(40), binary.LittleEndian.Uint32(wav[4:]))
	require.Equal(s.T(), "WAVEfmt ", string(wav[8:16]))
	require.Equal(s.T(), uint32(SampleRate), binary.LittleEndian.Uint32(wav[24:]))
	require.Equal(s.T(), "data", string(wav[36:40]))
	require.Equal(s.T(), uint32(4), binary.LittleEndian.Uint32(wav[40:]))
	require.Equal(s.T(), int16(-2), int16(binary.LittleEndian.Uint16(wav[46:])))
}

func (s *EngineSuite) TestTranscribe() {
	var got *http.Request
	var audio []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(s.T(), r.ParseMultipartForm(1<<24))
		got = r
		f, _, err := r.FormFile("file")
		require.NoError(s.T(), err)
		audio, _ = io.ReadAll(f)
		_, _ = w.Write([]byte(`{"text":" hello there\n","detected_language":"romanian","detected_language_probability":0.97}`))
	}))
	defer ts.Close()

	t, err := NewEngine(ts.Client()).Transcribe(context.Background(), ts.URL, make([]int16, SampleRate), "ro", "before")

	require.NoError(s.T(), err)
	require.Equal(s.T(), Transcript{Text: " hello there\n", Language: "romanian", Probability: 0.97}, t)
	require.Equal(s.T(), "/inference", got.URL.Path)
	require.Equal(s.T(), "ro", got.FormValue("language"))
	require.Equal(s.T(), "before", got.FormValue("prompt"))
	require.Equal(s.T(), "0", got.FormValue("temperature"))
	require.Equal(s.T(), "verbose_json", got.FormValue("response_format"))
	require.Equal(s.T(), "512", got.FormValue("audio_ctx"))
	// One second of audio is padded to two.
	require.Len(s.T(), audio, 44+minSamples*2)
}

func (s *EngineSuite) TestTranscribeErrors() {
	tests := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{name: "server error", status: http.StatusInternalServerError, body: `{"text":""}`, want: "HTTP 500"},
		{name: "error field", status: http.StatusOK, body: `{"error":"bad wav"}`, want: "bad wav"},
		{name: "not json", status: http.StatusOK, body: `nope`, want: "nope"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer ts.Close()
			_, err := NewEngine(ts.Client()).Transcribe(context.Background(), ts.URL, make([]int16, minSamples), "en", "")
			require.ErrorContains(s.T(), err, tc.want)
		})
	}

	_, err := NewEngine(failingClient{}).Transcribe(context.Background(), "http://x", nil, "en", "")
	require.ErrorContains(s.T(), err, "offline")
	_, err = NewEngine(failingClient{}).Transcribe(context.Background(), "http://\x7f", nil, "en", "")
	require.ErrorContains(s.T(), err, "invalid control character")
}
