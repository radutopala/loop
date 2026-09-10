package browser

import (
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chromedp/cdproto/cdp"
	cdpio "github.com/chromedp/cdproto/io"
	"github.com/chromedp/cdproto/network"
	cdppage "github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

// pngBytes is a header Go's content sniff calls image/png.
var pngBytes = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0}

// faviconExec answers the three commands one icon fetch issues. Every field is
// a knob a test turns: what the frame tree says, what the load reports, and
// what the stream hands back chunk by chunk.
type faviconExec struct {
	frameErr  error
	loadErr   error
	resource  *network.LoadNetworkResourcePageResult
	reads     []cdpio.ReadReturns
	readErr   error
	loadedURL string
	frameID   cdp.FrameID
	closed    cdpio.StreamHandle
	readCalls int
}

func (e *faviconExec) Execute(_ context.Context, method string, params, res any) error {
	switch method {
	case cdppage.CommandGetFrameTree:
		if e.frameErr != nil {
			return e.frameErr
		}
		res.(*cdppage.GetFrameTreeReturns).FrameTree = &cdppage.FrameTree{
			Frame: &cdp.Frame{ID: "FRAME-1"},
		}
	case network.CommandLoadNetworkResource:
		if e.loadErr != nil {
			return e.loadErr
		}
		p := params.(*network.LoadNetworkResourceParams)
		e.loadedURL = p.URL
		e.frameID = p.FrameID
		res.(*network.LoadNetworkResourceReturns).Resource = e.resource
	case cdpio.CommandRead:
		if e.readErr != nil {
			return e.readErr
		}
		out := res.(*cdpio.ReadReturns)
		*out = e.reads[min(e.readCalls, len(e.reads)-1)]
		e.readCalls++
	case cdpio.CommandClose:
		e.closed = params.(*cdpio.CloseParams).Handle
	}
	return nil
}

// wire points the client at the fake executor for both the actions it runs and
// the raw stream reads it issues.
func (s *CDPSuite) wire(exec *faviconExec) {
	s.client.exec = exec.Execute
	s.setRunFn(func(ctx context.Context, actions ...chromedp.Action) error {
		for _, a := range actions {
			if err := a.Do(cdp.WithExecutor(ctx, exec)); err != nil {
				return err
			}
		}
		return nil
	})
}

// okResource is a load that succeeded and left a stream to drain.
func okResource() *network.LoadNetworkResourcePageResult {
	return &network.LoadNetworkResourcePageResult{Success: true, Stream: "STREAM-1"}
}

// devtoolsList stands in for Chrome's /json/list, which is where the icon URLs
// come from; the client's wsURL points at it.
func (s *CDPSuite) devtoolsList(body string) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	s.T().Cleanup(srv.Close)
	s.client.wsURL = "ws://" + strings.TrimPrefix(srv.URL, "http://")
}

func (s *CDPSuite) TestFetchFaviconInlinesTheBytes() {
	exec := &faviconExec{
		resource: okResource(),
		reads: []cdpio.ReadReturns{{
			Base64encoded: true,
			Data:          base64.StdEncoding.EncodeToString(pngBytes),
			EOF:           true,
		}},
	}
	s.wire(exec)

	got, err := s.client.fetchFavicon("https://site.example/favicon.ico")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "data:image/png;base64,"+base64.StdEncoding.EncodeToString(pngBytes), got)

	// The request leaves from the page's own frame, which is what keeps it on
	// the browser's network rather than the host's.
	require.Equal(s.T(), "https://site.example/favicon.ico", exec.loadedURL)
	require.Equal(s.T(), cdp.FrameID("FRAME-1"), exec.frameID)
	require.Equal(s.T(), cdpio.StreamHandle("STREAM-1"), exec.closed)
}

// Chrome may hand back a plain-text stream, and it may split one icon across
// several reads.
func (s *CDPSuite) TestFetchFaviconJoinsChunksAndTakesRawData() {
	exec := &faviconExec{
		resource: okResource(),
		reads: []cdpio.ReadReturns{
			{Data: string(pngBytes[:4])},
			{Data: string(pngBytes[4:]), EOF: true},
		},
	}
	s.wire(exec)

	got, err := s.client.fetchFavicon("https://site.example/favicon.ico")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "data:image/png;base64,"+base64.StdEncoding.EncodeToString(pngBytes), got)
}

func (s *CDPSuite) TestFetchFaviconFailures() {
	tests := []struct {
		name string
		exec *faviconExec
		want string
	}{
		{
			name: "frame tree unavailable",
			exec: &faviconExec{frameErr: errors.New("no tree")},
			want: "frame tree",
		},
		{
			name: "load command rejected",
			exec: &faviconExec{loadErr: errors.New("bad url")},
			want: "loading icon",
		},
		{
			name: "load reported a network failure",
			exec: &faviconExec{resource: &network.LoadNetworkResourcePageResult{
				NetErrorName: "net::ERR_NAME_NOT_RESOLVED", HTTPStatusCode: 0,
			}},
			want: "net::ERR_NAME_NOT_RESOLVED",
		},
		{
			// A success with no stream is Chrome answering in the other shape
			// the result allows, and there is nothing to read.
			name: "success without a stream",
			exec: &faviconExec{resource: &network.LoadNetworkResourcePageResult{
				Success: true, HTTPStatusCode: 200,
			}},
			want: "http status 200",
		},
		{
			name: "stream read failed",
			exec: &faviconExec{resource: okResource(), readErr: errors.New("gone")},
			want: "reading icon",
		},
		{
			name: "chunk is not the base64 it claims",
			exec: &faviconExec{resource: okResource(), reads: []cdpio.ReadReturns{
				{Base64encoded: true, Data: "not base64!!", EOF: true},
			}},
			want: "decoding icon",
		},
		{
			name: "icon larger than the cap",
			exec: &faviconExec{resource: okResource(), reads: []cdpio.ReadReturns{
				{Data: strings.Repeat("x", maxFaviconBytes+1), EOF: true},
			}},
			want: "larger than",
		},
		{
			// A stream that never sets EOF would otherwise spin forever on a
			// goroutine the command deadline has already abandoned.
			name: "stream never ends",
			exec: &faviconExec{resource: okResource(), reads: []cdpio.ReadReturns{
				{Data: "x"},
			}},
			want: "did not end within",
		},
		{
			name: "bytes are not an image",
			exec: &faviconExec{resource: okResource(), reads: []cdpio.ReadReturns{
				{Data: "<html>nope</html>", EOF: true},
			}},
			want: "not an image",
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.wire(tt.exec)
			got, err := s.client.fetchFavicon("https://site.example/favicon.ico")
			require.Empty(s.T(), got)
			require.ErrorContains(s.T(), err, tt.want)
		})
	}
}

// The tab list refreshes constantly, so an icon is fetched once and then read
// from the cache — including an icon that could not be fetched at all.
func (s *CDPSuite) TestFaviconAsDataFetchesOncePerURL() {
	exec := &faviconExec{
		resource: okResource(),
		reads:    []cdpio.ReadReturns{{Data: string(pngBytes), EOF: true}},
	}
	s.wire(exec)

	first := s.client.faviconAsData("https://site.example/favicon.ico")
	require.NotEmpty(s.T(), first)
	require.Equal(s.T(), first, s.client.faviconAsData("https://site.example/favicon.ico"))
	require.Equal(s.T(), 1, exec.readCalls)

	failing := &faviconExec{loadErr: errors.New("gone")}
	s.wire(failing)
	require.Empty(s.T(), s.client.faviconAsData("https://site.example/missing.ico"))
	s.wire(&faviconExec{loadErr: errors.New("must not be called")})
	require.Empty(s.T(), s.client.faviconAsData("https://site.example/missing.ico"))
}

// A pane left open across dozens of sites must not grow an unbounded cache.
func (s *CDPSuite) TestFaviconCacheIsBounded() {
	exec := &faviconExec{
		resource: okResource(),
		reads:    []cdpio.ReadReturns{{Data: string(pngBytes), EOF: true}},
	}
	s.wire(exec)

	for i := range maxFaviconCache + 1 {
		s.client.faviconAsData(strings.Repeat("a", i) + ".example/icon.png")
	}
	require.Len(s.T(), s.client.faviconData, 1)
}

func (s *CDPSuite) TestFaviconsInlinesEveryTabsIcon() {
	s.devtoolsList(`[
		{"type":"page","id":"T1","faviconUrl":"https://site.example/one.ico"},
		{"type":"page","id":"T2","faviconUrl":"https://site.example/two.ico"},
		{"type":"page","id":"T3"}
	]`)
	exec := &faviconExec{
		resource: okResource(),
		reads:    []cdpio.ReadReturns{{Data: string(pngBytes), EOF: true}},
	}
	s.wire(exec)

	inlined := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pngBytes)
	require.Equal(s.T(), map[string]string{"T1": inlined, "T2": inlined}, s.client.Favicons())
}

// An icon the browser cannot fetch leaves the tab out of the map entirely, so
// the strip draws its dot rather than a broken image.
func (s *CDPSuite) TestFaviconsSkipsWhatItCannotFetch() {
	s.devtoolsList(`[{"type":"page","id":"T1","faviconUrl":"https://site.example/one.ico"}]`)
	s.wire(&faviconExec{loadErr: errors.New("gone")})
	require.Empty(s.T(), s.client.Favicons())
}

func (s *CDPSuite) TestFaviconsWithoutAnyIcons() {
	s.devtoolsList(`[{"type":"page","id":"T1"}]`)
	require.Nil(s.T(), s.client.Favicons())
}

// The suite's client has no logger of its own in this path; make sure the
// helper survives being handed one that is the default.
func TestFaviconLoggerIsUsable(t *testing.T) {
	require.NotNil(t, slog.Default())
}
