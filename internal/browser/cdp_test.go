package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/cdp"
	cdpdom "github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/network"
	cdppage "github.com/chromedp/cdproto/page"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/go-json-experiment/json/jsontext"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type CDPSuite struct {
	suite.Suite
	client *CDPClient
}

func TestCDPSuite(t *testing.T) {
	suite.Run(t, new(CDPSuite))
}

func (s *CDPSuite) SetupTest() {
	noopRun := func(_ context.Context, _ ...chromedp.Action) error {
		return nil
	}

	s.client = &CDPClient{
		ctx:          context.Background(),
		logger:       slog.Default(),
		runFn:        noopRun,
		targetsFunc:  func(_ context.Context) ([]*target.Info, error) { return nil, nil },
		listenFunc:   func(_ context.Context, _ func(any)) {},
		axTreeFunc:   func(_ context.Context) ([]*accessibility.Node, error) { return nil, nil },
		boxModelFunc: func(_ context.Context, _ cdp.BackendNodeID) (*cdpdom.BoxModel, error) { return nil, nil },
		createTabFunc: func(_ context.Context, _ string) (target.ID, error) {
			return "", nil
		},
		activateFunc: func(_ context.Context, _ target.ID) error { return nil },
		frameCh:      make(chan []byte, 2),
		stopCh:       make(chan struct{}),
		allocCancel:  func() {},
		ctxCancel:    func() {},
	}
}

// setRunFn sets the test client's runFn.
func (s *CDPSuite) setRunFn(fn func(context.Context, ...chromedp.Action) error) {
	s.client.runFn = fn
}

// --- NewCDPClient ---

func (s *CDPSuite) TestNewCDPClientSuccess() {
	c, err := NewCDPClient(context.Background(), "ws://test:9222", slog.Default(),
		WithAllocator(func(parent context.Context, _ string) (context.Context, context.CancelFunc) {
			return context.WithCancel(parent)
		}),
		WithRunFunc(func(_ context.Context, _ ...chromedp.Action) error { return nil }),
	)
	require.NoError(s.T(), err)
	require.NotNil(s.T(), c)
	// Verify that struct fields are populated with defaults.
	require.NotNil(s.T(), c.targetsFunc)
	require.NotNil(s.T(), c.listenFunc)
	require.NotNil(s.T(), c.axTreeFunc)
	require.NotNil(s.T(), c.boxModelFunc)
	require.NotNil(s.T(), c.createTabFunc)
	require.NotNil(s.T(), c.activateFunc)
	c.Close()
}

func (s *CDPSuite) TestNewCDPClientRunError() {
	c, err := NewCDPClient(context.Background(), "ws://test:9222", slog.Default(),
		WithAllocator(func(parent context.Context, _ string) (context.Context, context.CancelFunc) {
			return context.WithCancel(parent)
		}),
		WithRunFunc(func(_ context.Context, _ ...chromedp.Action) error {
			return errors.New("connection refused")
		}),
	)
	require.Error(s.T(), err)
	require.Nil(s.T(), c)
	require.Contains(s.T(), err.Error(), "connecting to CDP")
}

func (s *CDPSuite) TestNewCDPClientDefaults() {
	// Exercise the constructor with real defaults (no options).
	// Will fail to connect (no real Chrome), but covers default allocator and runFunc bodies.
	c, err := NewCDPClient(context.Background(), "ws://127.0.0.1:9222", slog.Default())
	require.Error(s.T(), err)
	require.Nil(s.T(), c)
}

func (s *CDPSuite) TestNewCDPClientDefaultFieldBodies() {
	// Construct via NewCDPClient with mock alloc/run, then call each default field
	// to cover the closure bodies set by the constructor.
	c, err := NewCDPClient(context.Background(), "ws://test:9222", slog.Default(),
		WithAllocator(func(parent context.Context, _ string) (context.Context, context.CancelFunc) {
			return context.WithCancel(parent)
		}),
		WithRunFunc(func(_ context.Context, _ ...chromedp.Action) error { return nil }),
	)
	require.NoError(s.T(), err)
	defer c.Close()

	// Each call exercises the default closure body — they'll fail (no real CDP), that's fine.
	_, _ = c.targetsFunc(context.Background())
	require.Panics(s.T(), func() {
		c.listenFunc(context.Background(), func(_ any) {})
	})
	_, _ = c.axTreeFunc(context.Background())
	_, _ = c.boxModelFunc(context.Background(), 0)
	_, _ = c.createTabFunc(context.Background(), "about:blank")
	_ = c.activateFunc(context.Background(), "")
}

// --- Close ---

func (s *CDPSuite) TestCloseNotScreencasting() {
	cancelCalled := false
	s.client.ctxCancel = func() { cancelCalled = true }
	s.client.Close()
	require.True(s.T(), cancelCalled)
}

func (s *CDPSuite) TestCloseWhileScreencasting() {
	s.client.screencasting = true
	stopCh := make(chan struct{})
	s.client.stopCh = stopCh

	s.client.Close()

	select {
	case <-stopCh:
		// OK — stopCh was closed
	default:
		s.T().Fatal("stopCh should be closed")
	}
}

// --- Navigate ---

func (s *CDPSuite) TestNavigateSuccess() {
	err := s.client.Navigate(context.Background(), "https://example.com")
	require.NoError(s.T(), err)
}

func (s *CDPSuite) TestNavigateError() {
	s.setRunFn(func(_ context.Context, _ ...chromedp.Action) error {
		return errors.New("nav error")
	})
	err := s.client.Navigate(context.Background(), "https://example.com")
	require.Error(s.T(), err)
}

// --- Reload ---

func (s *CDPSuite) TestReloadSuccess() {
	require.NoError(s.T(), s.client.Reload(context.Background()))
}

func (s *CDPSuite) TestReloadError() {
	s.setRunFn(func(_ context.Context, _ ...chromedp.Action) error { return errors.New("fail") })
	require.Error(s.T(), s.client.Reload(context.Background()))
}

// --- GoBack ---

func (s *CDPSuite) TestGoBackSuccess() {
	require.NoError(s.T(), s.client.GoBack(context.Background()))
}

func (s *CDPSuite) TestGoBackError() {
	s.setRunFn(func(_ context.Context, _ ...chromedp.Action) error { return errors.New("fail") })
	require.Error(s.T(), s.client.GoBack(context.Background()))
}

// --- GoForward ---

func (s *CDPSuite) TestGoForwardSuccess() {
	require.NoError(s.T(), s.client.GoForward(context.Background()))
}

func (s *CDPSuite) TestGoForwardError() {
	s.setRunFn(func(_ context.Context, _ ...chromedp.Action) error { return errors.New("fail") })
	require.Error(s.T(), s.client.GoForward(context.Background()))
}

// --- GetPageInfo ---

func (s *CDPSuite) TestGetPageInfoSuccess() {
	info, err := s.client.GetPageInfo(context.Background())
	require.NoError(s.T(), err)
	require.NotNil(s.T(), info)
}

func (s *CDPSuite) TestGetPageInfoError() {
	s.setRunFn(func(_ context.Context, _ ...chromedp.Action) error { return errors.New("fail") })
	info, err := s.client.GetPageInfo(context.Background())
	require.Error(s.T(), err)
	require.Nil(s.T(), info)
	require.Contains(s.T(), err.Error(), "getting page info")
}

// --- MouseClick ---

func (s *CDPSuite) TestMouseClickLeft() {
	require.NoError(s.T(), s.client.MouseClick(context.Background(), 100, 200, "left", 1))
}

func (s *CDPSuite) TestMouseClickRight() {
	require.NoError(s.T(), s.client.MouseClick(context.Background(), 100, 200, "right", 1))
}

func (s *CDPSuite) TestMouseClickMiddle() {
	require.NoError(s.T(), s.client.MouseClick(context.Background(), 100, 200, "middle", 1))
}

func (s *CDPSuite) TestMouseClickDefaultButton() {
	require.NoError(s.T(), s.client.MouseClick(context.Background(), 100, 200, "", 1))
}

func (s *CDPSuite) TestMouseClickError() {
	s.setRunFn(func(_ context.Context, _ ...chromedp.Action) error { return errors.New("fail") })
	require.Error(s.T(), s.client.MouseClick(context.Background(), 100, 200, "left", 1))
}

// --- MouseMove ---

func (s *CDPSuite) TestMouseMoveSuccess() {
	require.NoError(s.T(), s.client.MouseMove(context.Background(), 50, 60))
}

func (s *CDPSuite) TestMouseMoveError() {
	s.setRunFn(func(_ context.Context, _ ...chromedp.Action) error { return errors.New("fail") })
	require.Error(s.T(), s.client.MouseMove(context.Background(), 50, 60))
}

// --- MouseScroll ---

func (s *CDPSuite) TestMouseScrollSuccess() {
	require.NoError(s.T(), s.client.MouseScroll(context.Background(), 10, 20, 0, -120))
}

func (s *CDPSuite) TestMouseScrollError() {
	s.setRunFn(func(_ context.Context, _ ...chromedp.Action) error { return errors.New("fail") })
	require.Error(s.T(), s.client.MouseScroll(context.Background(), 10, 20, 0, -120))
}

// --- KeyPress ---

func (s *CDPSuite) TestKeyPressSuccess() {
	require.NoError(s.T(), s.client.KeyPress(context.Background(), "Enter"))
}

func (s *CDPSuite) TestKeyPressError() {
	s.setRunFn(func(_ context.Context, _ ...chromedp.Action) error { return errors.New("fail") })
	require.Error(s.T(), s.client.KeyPress(context.Background(), "Enter"))
}

// --- TypeText ---

func (s *CDPSuite) TestTypeTextSuccess() {
	require.NoError(s.T(), s.client.TypeText(context.Background(), "hello"))
}

func (s *CDPSuite) TestTypeTextEmpty() {
	require.NoError(s.T(), s.client.TypeText(context.Background(), ""))
}

func (s *CDPSuite) TestTypeTextError() {
	s.setRunFn(func(_ context.Context, _ ...chromedp.Action) error { return errors.New("fail") })
	err := s.client.TypeText(context.Background(), "ab")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "typing character")
}

// --- Screenshot ---

func (s *CDPSuite) TestScreenshotSuccess() {
	buf, err := s.client.Screenshot(context.Background())
	require.NoError(s.T(), err)
	// buf is nil because mock doesn't fill it, but no error
	_ = buf
}

func (s *CDPSuite) TestScreenshotError() {
	s.setRunFn(func(_ context.Context, _ ...chromedp.Action) error { return errors.New("fail") })
	_, err := s.client.Screenshot(context.Background())
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "capturing screenshot")
}

// --- ListTabs ---

func (s *CDPSuite) TestListTabsSuccess() {
	s.client.targetsFunc = func(_ context.Context) ([]*target.Info, error) {
		return []*target.Info{
			{TargetID: "t1", URL: "https://a.com", Title: "A", Type: "page"},
			{TargetID: "t2", URL: "about:blank", Title: "", Type: "background_page"},
			{TargetID: "t3", URL: "https://b.com", Title: "B", Type: "page"},
		}, nil
	}

	tabs, err := s.client.ListTabs(context.Background())
	require.NoError(s.T(), err)
	require.Len(s.T(), tabs, 2)
	require.Equal(s.T(), "t1", tabs[0].TargetID)
	require.Equal(s.T(), "t3", tabs[1].TargetID)
}

func (s *CDPSuite) TestListTabsEmpty() {
	s.client.targetsFunc = func(_ context.Context) ([]*target.Info, error) {
		return nil, nil
	}
	tabs, err := s.client.ListTabs(context.Background())
	require.NoError(s.T(), err)
	require.Empty(s.T(), tabs)
}

func (s *CDPSuite) TestListTabsError() {
	s.client.targetsFunc = func(_ context.Context) ([]*target.Info, error) {
		return nil, errors.New("fail")
	}
	_, err := s.client.ListTabs(context.Background())
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "listing targets")
}

// --- NewTab ---

func (s *CDPSuite) TestNewTabSuccess() {
	s.client.createTabFunc = func(_ context.Context, url string) (target.ID, error) {
		return target.ID("new-target"), nil
	}
	id, err := s.client.NewTab(context.Background(), "https://example.com")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "new-target", id)
}

func (s *CDPSuite) TestNewTabError() {
	s.client.createTabFunc = func(_ context.Context, _ string) (target.ID, error) {
		return "", errors.New("fail")
	}
	_, err := s.client.NewTab(context.Background(), "https://example.com")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating new tab")
}

// --- SwitchTab ---

func (s *CDPSuite) TestSwitchTabSuccess() {
	s.client.activateFunc = func(_ context.Context, _ target.ID) error { return nil }
	require.NoError(s.T(), s.client.SwitchTab(context.Background(), "t1"))
}

func (s *CDPSuite) TestSwitchTabError() {
	s.client.activateFunc = func(_ context.Context, _ target.ID) error { return errors.New("fail") }
	require.Error(s.T(), s.client.SwitchTab(context.Background(), "t1"))
}

// --- CloseTab ---

func (s *CDPSuite) TestCloseTabSuccess() {
	require.NoError(s.T(), s.client.CloseTab(context.Background(), "t1"))
}

func (s *CDPSuite) TestCloseTabError() {
	s.setRunFn(func(_ context.Context, _ ...chromedp.Action) error { return errors.New("fail") })
	require.Error(s.T(), s.client.CloseTab(context.Background(), "t1"))
}

func (s *CDPSuite) TestCloseTabHTTP() {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	s.client.wsURL = strings.Replace(srv.URL, "http://", "ws://", 1)

	err := s.client.CloseTab(context.Background(), "target-123")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "/json/close/target-123", gotPath)
}

func (s *CDPSuite) TestCloseTabHTTPError() {
	s.client.wsURL = "ws://127.0.0.1:1"
	err := s.client.CloseTab(context.Background(), "t1")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "closing tab t1")
}

// --- EvaluateJS ---

func (s *CDPSuite) TestEvaluateJSSuccess() {
	result, err := s.client.EvaluateJS(context.Background(), "1+1")
	require.NoError(s.T(), err)
	_ = result
}

func (s *CDPSuite) TestEvaluateJSError() {
	s.setRunFn(func(_ context.Context, _ ...chromedp.Action) error { return errors.New("fail") })
	_, err := s.client.EvaluateJS(context.Background(), "bad")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "evaluating JS")
}

// --- ClickRef ---

func (s *CDPSuite) TestClickRefSuccess() {
	refs := []ElementRef{
		{RefID: "ref_1", X: 10, Y: 20, Width: 100, Height: 50},
	}
	require.NoError(s.T(), s.client.ClickRef(context.Background(), refs, 1))
}

func (s *CDPSuite) TestClickRefOutOfRangeLow() {
	refs := []ElementRef{{RefID: "ref_1"}}
	require.Error(s.T(), s.client.ClickRef(context.Background(), refs, 0))
}

func (s *CDPSuite) TestClickRefOutOfRangeHigh() {
	refs := []ElementRef{{RefID: "ref_1"}}
	err := s.client.ClickRef(context.Background(), refs, 5)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "out of range")
}

func (s *CDPSuite) TestClickRefError() {
	s.setRunFn(func(_ context.Context, _ ...chromedp.Action) error { return errors.New("fail") })
	refs := []ElementRef{{RefID: "ref_1", X: 10, Y: 20, Width: 100, Height: 50}}
	require.Error(s.T(), s.client.ClickRef(context.Background(), refs, 1))
}

// --- GetElementRefs ---

func (s *CDPSuite) TestGetElementRefsSuccess() {
	s.client.axTreeFunc = func(_ context.Context) ([]*accessibility.Node, error) {
		return []*accessibility.Node{
			{
				Role:             &accessibility.Value{Value: jsontext.Value("button")},
				Name:             &accessibility.Value{Value: jsontext.Value("Submit")},
				Description:      &accessibility.Value{Value: jsontext.Value("Submit form")},
				Value:            &accessibility.Value{Value: jsontext.Value("val")},
				BackendDOMNodeID: cdp.BackendNodeID(1),
			},
		}, nil
	}
	s.client.boxModelFunc = func(_ context.Context, _ cdp.BackendNodeID) (*cdpdom.BoxModel, error) {
		return &cdpdom.BoxModel{
			Content: []float64{10, 20, 110, 20, 110, 70, 10, 70},
		}, nil
	}

	refs, err := s.client.GetElementRefs(context.Background())
	require.NoError(s.T(), err)
	require.Len(s.T(), refs, 1)
	require.Equal(s.T(), "ref_1", refs[0].RefID)
	require.Equal(s.T(), "button", refs[0].Role)
	require.Equal(s.T(), "Submit", refs[0].Name)
	require.Equal(s.T(), "Submit form", refs[0].Description)
	require.Equal(s.T(), "val", refs[0].Value)
	require.Equal(s.T(), float64(10), refs[0].X)
	require.Equal(s.T(), float64(20), refs[0].Y)
	require.Equal(s.T(), float64(100), refs[0].Width)
	require.Equal(s.T(), float64(50), refs[0].Height)
}

func (s *CDPSuite) TestGetElementRefsTreeError() {
	s.client.axTreeFunc = func(_ context.Context) ([]*accessibility.Node, error) {
		return nil, errors.New("tree error")
	}
	_, err := s.client.GetElementRefs(context.Background())
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "getting accessibility tree")
}

func (s *CDPSuite) TestGetElementRefsSkipsIgnored() {
	s.client.axTreeFunc = func(_ context.Context) ([]*accessibility.Node, error) {
		return []*accessibility.Node{
			{Ignored: true, Role: &accessibility.Value{Value: jsontext.Value("button")}},
		}, nil
	}
	refs, err := s.client.GetElementRefs(context.Background())
	require.NoError(s.T(), err)
	require.Empty(s.T(), refs)
}

func (s *CDPSuite) TestGetElementRefsSkipsNilRole() {
	s.client.axTreeFunc = func(_ context.Context) ([]*accessibility.Node, error) {
		return []*accessibility.Node{
			{Role: nil},
		}, nil
	}
	refs, err := s.client.GetElementRefs(context.Background())
	require.NoError(s.T(), err)
	require.Empty(s.T(), refs)
}

func (s *CDPSuite) TestGetElementRefsSkipsNonInteractive() {
	s.client.axTreeFunc = func(_ context.Context) ([]*accessibility.Node, error) {
		return []*accessibility.Node{
			{Role: &accessibility.Value{Value: jsontext.Value("heading")}, BackendDOMNodeID: 1},
		}, nil
	}
	refs, err := s.client.GetElementRefs(context.Background())
	require.NoError(s.T(), err)
	require.Empty(s.T(), refs)
}

func (s *CDPSuite) TestGetElementRefsSkipsZeroBackendNodeID() {
	s.client.axTreeFunc = func(_ context.Context) ([]*accessibility.Node, error) {
		return []*accessibility.Node{
			{Role: &accessibility.Value{Value: jsontext.Value("button")}, BackendDOMNodeID: 0},
		}, nil
	}
	refs, err := s.client.GetElementRefs(context.Background())
	require.NoError(s.T(), err)
	require.Empty(s.T(), refs)
}

func (s *CDPSuite) TestGetElementRefsSkipsBoxModelError() {
	s.client.axTreeFunc = func(_ context.Context) ([]*accessibility.Node, error) {
		return []*accessibility.Node{
			{Role: &accessibility.Value{Value: jsontext.Value("button")}, BackendDOMNodeID: 1},
		}, nil
	}
	s.client.boxModelFunc = func(_ context.Context, _ cdp.BackendNodeID) (*cdpdom.BoxModel, error) {
		return nil, errors.New("not visible")
	}
	refs, err := s.client.GetElementRefs(context.Background())
	require.NoError(s.T(), err)
	require.Empty(s.T(), refs)
}

func (s *CDPSuite) TestGetElementRefsSkipsNilBoxModel() {
	s.client.axTreeFunc = func(_ context.Context) ([]*accessibility.Node, error) {
		return []*accessibility.Node{
			{Role: &accessibility.Value{Value: jsontext.Value("button")}, BackendDOMNodeID: 1},
		}, nil
	}
	s.client.boxModelFunc = func(_ context.Context, _ cdp.BackendNodeID) (*cdpdom.BoxModel, error) {
		return nil, nil
	}
	refs, err := s.client.GetElementRefs(context.Background())
	require.NoError(s.T(), err)
	require.Empty(s.T(), refs)
}

func (s *CDPSuite) TestGetElementRefsSkipsSmallContent() {
	s.client.axTreeFunc = func(_ context.Context) ([]*accessibility.Node, error) {
		return []*accessibility.Node{
			{Role: &accessibility.Value{Value: jsontext.Value("button")}, BackendDOMNodeID: 1},
		}, nil
	}
	s.client.boxModelFunc = func(_ context.Context, _ cdp.BackendNodeID) (*cdpdom.BoxModel, error) {
		return &cdpdom.BoxModel{Content: []float64{0, 0}}, nil
	}
	refs, err := s.client.GetElementRefs(context.Background())
	require.NoError(s.T(), err)
	require.Empty(s.T(), refs)
}

func (s *CDPSuite) TestGetElementRefsSkipsZeroSize() {
	s.client.axTreeFunc = func(_ context.Context) ([]*accessibility.Node, error) {
		return []*accessibility.Node{
			{Role: &accessibility.Value{Value: jsontext.Value("button")}, BackendDOMNodeID: 1},
		}, nil
	}
	s.client.boxModelFunc = func(_ context.Context, _ cdp.BackendNodeID) (*cdpdom.BoxModel, error) {
		// Width = 0 (x2-x1 = 10-10 = 0)
		return &cdpdom.BoxModel{Content: []float64{10, 20, 10, 20, 10, 20, 10, 20}}, nil
	}
	refs, err := s.client.GetElementRefs(context.Background())
	require.NoError(s.T(), err)
	require.Empty(s.T(), refs)
}

func (s *CDPSuite) TestGetElementRefsNilNameDescValue() {
	s.client.axTreeFunc = func(_ context.Context) ([]*accessibility.Node, error) {
		return []*accessibility.Node{
			{
				Role:             &accessibility.Value{Value: jsontext.Value("button")},
				Name:             nil,
				Description:      nil,
				Value:            nil,
				BackendDOMNodeID: 1,
			},
		}, nil
	}
	s.client.boxModelFunc = func(_ context.Context, _ cdp.BackendNodeID) (*cdpdom.BoxModel, error) {
		return &cdpdom.BoxModel{Content: []float64{0, 0, 100, 0, 100, 50, 0, 50}}, nil
	}
	refs, err := s.client.GetElementRefs(context.Background())
	require.NoError(s.T(), err)
	require.Len(s.T(), refs, 1)
	require.Empty(s.T(), refs[0].Name)
	require.Empty(s.T(), refs[0].Description)
	require.Empty(s.T(), refs[0].Value)
}

// --- StartScreencast ---

func (s *CDPSuite) TestStartScreencastAlreadyScreencasting() {
	s.client.screencasting = true
	ch := s.client.StartScreencast(60, 1920, 1080)
	require.NotNil(s.T(), ch)
	require.Equal(s.T(), (<-chan []byte)(s.client.frameCh), ch)
}

func (s *CDPSuite) TestStartScreencastNew() {
	var listenerFn func(any)
	s.client.listenFunc = func(_ context.Context, fn func(any)) {
		listenerFn = fn
	}

	ch := s.client.StartScreencast(60, 1920, 1080)
	require.NotNil(s.T(), ch)
	require.True(s.T(), s.client.screencasting)
	require.NotNil(s.T(), listenerFn)

	// Wait a bit for the screencast goroutine to run
	time.Sleep(10 * time.Millisecond)
}

func (s *CDPSuite) TestStartScreencastFrameDecodeSuccess() {
	var listenerFn func(any)
	s.client.listenFunc = func(_ context.Context, fn func(any)) {
		listenerFn = fn
	}

	ch := s.client.StartScreencast(60, 1920, 1080)
	require.NotNil(s.T(), listenerFn)

	// Simulate a screencast frame event with valid base64 data.
	frameData := []byte("jpeg-frame-data")
	encoded := base64.StdEncoding.EncodeToString(frameData)
	listenerFn(&cdppage.EventScreencastFrame{
		Data:      encoded,
		SessionID: 1,
	})

	// Read the frame from the channel.
	select {
	case data := <-ch:
		require.Equal(s.T(), frameData, data)
	case <-time.After(time.Second):
		s.T().Fatal("timeout waiting for frame")
	}
}

func (s *CDPSuite) TestStartScreencastFrameDecodeError() {
	var listenerFn func(any)
	s.client.listenFunc = func(_ context.Context, fn func(any)) {
		listenerFn = fn
	}

	_ = s.client.StartScreencast(60, 1920, 1080)

	// Simulate an event with invalid base64.
	listenerFn(&cdppage.EventScreencastFrame{
		Data:      "not-valid-base64!!!",
		SessionID: 1,
	})

	// No frame should be sent (decode error logged).
	select {
	case <-s.client.frameCh:
		s.T().Fatal("should not receive frame on decode error")
	case <-time.After(50 * time.Millisecond):
		// OK
	}
}

func (s *CDPSuite) TestStartScreencastFrameDropped() {
	var listenerFn func(any)
	s.client.listenFunc = func(_ context.Context, fn func(any)) {
		listenerFn = fn
	}

	_ = s.client.StartScreencast(60, 1920, 1080)

	// Fill the channel.
	encoded := base64.StdEncoding.EncodeToString([]byte("frame1"))
	listenerFn(&cdppage.EventScreencastFrame{Data: encoded, SessionID: 1})
	listenerFn(&cdppage.EventScreencastFrame{Data: encoded, SessionID: 2})

	// Third should be dropped (channel buffer = 2).
	listenerFn(&cdppage.EventScreencastFrame{Data: encoded, SessionID: 3})

	require.Len(s.T(), s.client.frameCh, 2)
}

func (s *CDPSuite) TestStartScreencastNonFrameEvent() {
	var listenerFn func(any)
	s.client.listenFunc = func(_ context.Context, fn func(any)) {
		listenerFn = fn
	}

	_ = s.client.StartScreencast(60, 1920, 1080)

	// Send a non-frame event — should be ignored.
	listenerFn("not a frame event")

	select {
	case <-s.client.frameCh:
		s.T().Fatal("should not receive anything for non-frame events")
	case <-time.After(50 * time.Millisecond):
		// OK
	}
}

func (s *CDPSuite) TestStartScreencastRunError() {
	s.client.listenFunc = func(_ context.Context, _ func(any)) {}
	s.setRunFn(func(_ context.Context, _ ...chromedp.Action) error {
		return errors.New("screencast start error")
	})

	_ = s.client.StartScreencast(60, 1920, 1080)
	// Error is logged, not returned. Wait for goroutine.
	time.Sleep(50 * time.Millisecond)
}

func (s *CDPSuite) TestStartScreencastAckError() {
	var listenerFn func(any)
	s.client.listenFunc = func(_ context.Context, fn func(any)) {
		listenerFn = fn
	}

	var mu sync.Mutex
	callCount := 0
	s.setRunFn(func(_ context.Context, _ ...chromedp.Action) error {
		mu.Lock()
		callCount++
		c := callCount
		mu.Unlock()
		if c > 1 { // First call is StartScreencast, second is Ack
			return errors.New("ack error")
		}
		return nil
	})

	_ = s.client.StartScreencast(60, 1920, 1080)
	time.Sleep(10 * time.Millisecond)
	require.NotNil(s.T(), listenerFn)

	encoded := base64.StdEncoding.EncodeToString([]byte("frame"))
	listenerFn(&cdppage.EventScreencastFrame{Data: encoded, SessionID: 1})
	time.Sleep(50 * time.Millisecond) // Wait for ack goroutine
}

// --- StopScreencast ---

func (s *CDPSuite) TestStopScreencastWasScreencasting() {
	s.client.screencasting = true
	stopCh := make(chan struct{})
	s.client.stopCh = stopCh

	s.client.StopScreencast()

	require.False(s.T(), s.client.screencasting)
	select {
	case <-stopCh:
		// OK
	default:
		s.T().Fatal("stopCh should be closed")
	}
}

func (s *CDPSuite) TestStopScreencastNotScreencasting() {
	s.client.screencasting = false
	s.client.StopScreencast()
	require.False(s.T(), s.client.screencasting)
}

// --- findOrCreatePageTarget ---

func (s *CDPSuite) TestFindPageTargetExistingPage() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json/list" {
			fmt.Fprint(w, `[{"id":"AAAA","type":"page"},{"id":"BBBB","type":"background_page"}]`)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1)
	tid, err := findOrCreatePageTarget(wsURL, true)
	require.NoError(s.T(), err)
	require.Equal(s.T(), target.ID("AAAA"), tid)
}

func (s *CDPSuite) TestFindPageTargetEmptyListCreatesNew() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/json/list" && r.Method == http.MethodGet:
			fmt.Fprint(w, `[]`)
		case r.URL.Path == "/json/new" && r.Method == http.MethodPut:
			fmt.Fprint(w, `{"id":"NEW-TARGET-ID"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1)
	tid, err := findOrCreatePageTarget(wsURL, true)
	require.NoError(s.T(), err)
	require.Equal(s.T(), target.ID("NEW-TARGET-ID"), tid)
}

func (s *CDPSuite) TestFindPageTargetHTTPErrorOnList() {
	// Use an unreachable URL so http.Get fails.
	_, err := findOrCreatePageTarget("ws://127.0.0.1:1", true) // port 1 is unlikely to respond
	require.Error(s.T(), err)
}

func (s *CDPSuite) TestFindPageTargetInvalidJSONFromList() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `NOT JSON`)
	}))
	defer srv.Close()

	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1)
	_, err := findOrCreatePageTarget(wsURL, true)
	require.Error(s.T(), err)
}

func (s *CDPSuite) TestFindPageTargetPutError() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json/list" {
			fmt.Fprint(w, `[]`) // no page targets
			return
		}
		// Close connection abruptly for PUT /json/new.
		hj, ok := w.(http.Hijacker)
		if ok {
			conn, _, _ := hj.Hijack()
			conn.Close()
		}
	}))
	defer srv.Close()

	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1)
	_, err := findOrCreatePageTarget(wsURL, true)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating new page target")
}

func (s *CDPSuite) TestFindPageTargetEmptyIDFromNew() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json/list":
			fmt.Fprint(w, `[]`)
		case "/json/new":
			fmt.Fprint(w, `{"id":""}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1)
	_, err := findOrCreatePageTarget(wsURL, true)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "empty target ID from /json/new")
}

func (s *CDPSuite) TestFindPageTargetInvalidJSONFromNew() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json/list":
			fmt.Fprint(w, `[]`)
		case "/json/new":
			fmt.Fprint(w, `NOT JSON`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1)
	_, err := findOrCreatePageTarget(wsURL, true)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "decoding new target")
}

func (s *CDPSuite) TestFindPageTargetNoPageTypeInList() {
	// List returns targets but none are "page" type → falls through to PUT /json/new.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json/list":
			fmt.Fprint(w, `[{"id":"X","type":"background_page"},{"id":"Y","type":"service_worker"}]`)
		case "/json/new":
			fmt.Fprint(w, `{"id":"CREATED"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1)
	tid, err := findOrCreatePageTarget(wsURL, true)
	require.NoError(s.T(), err)
	require.Equal(s.T(), target.ID("CREATED"), tid)
}

func (s *CDPSuite) TestFindPageTargetNewTargetSkipsList() {
	// With reuseExisting=false, /json/list should NOT be called — goes directly to /json/new.
	listCalled := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json/list":
			listCalled = true
			fmt.Fprint(w, `[{"id":"EXISTING","type":"page"}]`)
		case "/json/new":
			fmt.Fprint(w, `{"id":"FRESH"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1)
	tid, err := findOrCreatePageTarget(wsURL, false)
	require.NoError(s.T(), err)
	require.Equal(s.T(), target.ID("FRESH"), tid)
	require.False(s.T(), listCalled, "/json/list should not be called with reuseExisting=false")
}

// --- NewCDPClient with pre-set targetID ---

func (s *CDPSuite) TestNewCDPClientWithTargetID() {
	// Use a CDPOption that sets cfg.targetID directly to exercise the
	// "cfg.targetID != ''" branch in NewCDPClient.
	withTargetID := func(id target.ID) CDPOption {
		return func(c *cdpConfig) { c.targetID = id }
	}

	c, err := NewCDPClient(context.Background(), "ws://test:9222", slog.Default(),
		WithAllocator(func(parent context.Context, _ string) (context.Context, context.CancelFunc) {
			return context.WithCancel(parent)
		}),
		WithRunFunc(func(_ context.Context, _ ...chromedp.Action) error { return nil }),
		withTargetID("pre-set-target"),
	)
	require.NoError(s.T(), err)
	require.NotNil(s.T(), c)
	c.Close()
}

// --- NewCDPClient with findOrCreatePageTarget fallback ---

func (s *CDPSuite) TestNewCDPClientFindPageTargetFallback() {
	// Start a fake Chrome HTTP endpoint that returns a page target.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/json/list" {
			fmt.Fprint(w, `[{"id":"found-target","type":"page"}]`)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1)
	c, err := NewCDPClient(context.Background(), wsURL, slog.Default(),
		WithAllocator(func(parent context.Context, _ string) (context.Context, context.CancelFunc) {
			return context.WithCancel(parent)
		}),
		WithRunFunc(func(_ context.Context, _ ...chromedp.Action) error { return nil }),
	)
	require.NoError(s.T(), err)
	require.NotNil(s.T(), c)
	c.Close()
}

func (s *CDPSuite) TestNewCDPClientFindPageTargetError() {
	// When findOrCreatePageTarget fails, NewCDPClient should still succeed
	// (it just skips the WithTargetID option).
	c, err := NewCDPClient(context.Background(), "ws://127.0.0.1:1", slog.Default(),
		WithAllocator(func(parent context.Context, _ string) (context.Context, context.CancelFunc) {
			return context.WithCancel(parent)
		}),
		WithRunFunc(func(_ context.Context, _ ...chromedp.Action) error { return nil }),
	)
	require.NoError(s.T(), err)
	require.NotNil(s.T(), c)
	c.Close()
}

// --- TargetID ---

func (s *CDPSuite) TestTargetIDEmpty() {
	require.Equal(s.T(), "", s.client.TargetID())
}

func (s *CDPSuite) TestTargetIDSet() {
	s.client.targetID = target.ID("my-target-123")
	require.Equal(s.T(), "my-target-123", s.client.TargetID())
}

// --- WithTargetID option ---

func (s *CDPSuite) TestWithTargetIDOption() {
	c, err := NewCDPClient(context.Background(), "ws://test:9222", slog.Default(),
		WithAllocator(func(parent context.Context, _ string) (context.Context, context.CancelFunc) {
			return context.WithCancel(parent)
		}),
		WithRunFunc(func(_ context.Context, _ ...chromedp.Action) error { return nil }),
		WithTargetID("explicit-target-id"),
	)
	require.NoError(s.T(), err)
	require.NotNil(s.T(), c)
	require.Equal(s.T(), "explicit-target-id", c.TargetID())
	c.Close()
}

// --- WithNewTarget option ---

func (s *CDPSuite) TestWithNewTargetOption() {
	// Start a fake HTTP endpoint that returns a page target on /json/list
	// and a new target on /json/new.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json/list":
			// reuseTarget=false should skip this entirely.
			s.T().Fatal("/json/list should not be called with WithNewTarget")
		case "/json/new":
			fmt.Fprint(w, `{"id":"fresh-target"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	wsURL := strings.Replace(srv.URL, "http://", "ws://", 1)
	c, err := NewCDPClient(context.Background(), wsURL, slog.Default(),
		WithAllocator(func(parent context.Context, _ string) (context.Context, context.CancelFunc) {
			return context.WithCancel(parent)
		}),
		WithRunFunc(func(_ context.Context, _ ...chromedp.Action) error { return nil }),
		WithNewTarget(),
	)
	require.NoError(s.T(), err)
	require.NotNil(s.T(), c)
	require.Equal(s.T(), "fresh-target", c.TargetID())
	c.Close()
}

// --- GoBack / GoForward timeout path ---

// --- EnableConsoleCapture ---

func (s *CDPSuite) TestEnableConsoleCaptureSuccess() {
	ch := make(chan ConsoleMessage, 10)
	var capturedFn func(any)
	s.client.listenFunc = func(_ context.Context, fn func(any)) {
		capturedFn = fn
	}
	err := s.client.EnableConsoleCapture(context.Background(), ch)
	require.NoError(s.T(), err)
	require.NotNil(s.T(), capturedFn)

	// Simulate console events to cover the listener callback body.
	capturedFn(&cdpruntime.EventConsoleAPICalled{
		Type: "log",
		Args: []*cdpruntime.RemoteObject{
			{Value: jsontext.Value(`"hello world"`)},
		},
	})
	capturedFn(&cdpruntime.EventConsoleAPICalled{
		Type: "error",
		Args: []*cdpruntime.RemoteObject{
			{Description: "ReferenceError: x is not defined"},
		},
	})
	// Non-console event — should be ignored.
	capturedFn("not a console event")
	// Console event with empty args.
	capturedFn(&cdpruntime.EventConsoleAPICalled{
		Type: "warning",
		Args: []*cdpruntime.RemoteObject{{}},
	})

	require.Len(s.T(), ch, 3) // log, error, warning (empty text still sent)
	msg1 := <-ch
	require.Equal(s.T(), "log", msg1.Level)
	require.Equal(s.T(), "hello world", msg1.Text)
	msg2 := <-ch
	require.Equal(s.T(), "error", msg2.Level)
	require.Equal(s.T(), "ReferenceError: x is not defined", msg2.Text)
}

func (s *CDPSuite) TestEnableConsoleCaptureRunError() {
	ch := make(chan ConsoleMessage, 10)
	s.setRunFn(func(_ context.Context, _ ...chromedp.Action) error {
		return errors.New("runtime enable failed")
	})
	err := s.client.EnableConsoleCapture(context.Background(), ch)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "enabling runtime domain")
}

// --- axTreeLenient fallback path ---

func (s *CDPSuite) TestAxTreeLenientFallbackPath() {
	// Construct nodes with role, name, description, value.
	s.client.axTreeFunc = func(_ context.Context) ([]*accessibility.Node, error) {
		return []*accessibility.Node{
			{
				Role:             &accessibility.Value{Value: jsontext.Value(`"button"`)},
				Name:             &accessibility.Value{Value: jsontext.Value(`"Submit"`)},
				Description:      &accessibility.Value{Value: jsontext.Value(`"desc"`)},
				Value:            &accessibility.Value{Value: jsontext.Value(`"val"`)},
				BackendDOMNodeID: cdp.BackendNodeID(1),
			},
		}, nil
	}
	s.client.boxModelFunc = func(_ context.Context, _ cdp.BackendNodeID) (*cdpdom.BoxModel, error) {
		return &cdpdom.BoxModel{Content: []float64{0, 0, 100, 0, 100, 50, 0, 50}}, nil
	}

	refs, err := s.client.GetElementRefs(context.Background())
	require.NoError(s.T(), err)
	require.Len(s.T(), refs, 1)
	require.Equal(s.T(), "button", refs[0].Role)
	require.Equal(s.T(), "Submit", refs[0].Name)
	require.Equal(s.T(), "desc", refs[0].Description)
	require.Equal(s.T(), "val", refs[0].Value)
}

func (s *CDPSuite) TestNewCDPClientDefaultClosures() {
	// Create a CDPClient with WithExec so the default closures can be exercised.
	noopExec := func(_ context.Context, _ string, _, _ any) error { return nil }
	execRunFn := func(ctx context.Context, actions ...chromedp.Action) error {
		for _, a := range actions {
			if err := a.Do(ctx); err != nil {
				return err
			}
		}
		return nil
	}

	c, err := NewCDPClient(context.Background(), "ws://test:9222", slog.Default(),
		WithAllocator(func(parent context.Context, _ string) (context.Context, context.CancelFunc) {
			return context.WithCancel(parent)
		}),
		WithRunFunc(execRunFn),
		WithExec(noopExec),
	)
	require.NoError(s.T(), err)
	defer c.Close()

	// Exercise boxModelFunc (default closure via cfg.exec).
	_, err = c.boxModelFunc(context.Background(), cdp.BackendNodeID(1))
	require.NoError(s.T(), err)

	// Exercise createTabFunc.
	_, err = c.createTabFunc(context.Background(), "about:blank")
	require.NoError(s.T(), err)

	// Exercise activateFunc.
	err = c.activateFunc(context.Background(), target.ID("t1"))
	require.NoError(s.T(), err)

	// Exercise axTreeFunc.
	_, err = c.axTreeFunc(context.Background())
	require.NoError(s.T(), err)

	// Exercise EnableConsoleCapture.
	ch := make(chan ConsoleMessage, 1)
	c.listenFunc = func(_ context.Context, _ func(any)) {}
	err = c.EnableConsoleCapture(context.Background(), ch)
	require.NoError(s.T(), err)
}

func (s *CDPSuite) TestMakeAxTreeFuncRunFnError() {
	runFn := func(_ context.Context, _ ...chromedp.Action) error {
		return errors.New("run failed")
	}
	axFn := makeAxTreeFunc(runFn)
	_, err := axFn(context.Background())
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "run failed")
}

func (s *CDPSuite) TestMakeAxTreeFuncExecuteError() {
	// Inject a fake executor that returns an error.
	runFn := func(ctx context.Context, actions ...chromedp.Action) error {
		for _, a := range actions {
			return a.Do(ctx)
		}
		return nil
	}
	exec := func(_ context.Context, _ string, _, _ any) error {
		return errors.New("cdp execute failed")
	}
	axFn := makeAxTreeFuncWith(runFn, exec)
	_, err := axFn(context.Background())
	require.Error(s.T(), err)
}

func (s *CDPSuite) TestMakeAxTreeFuncStrictUnmarshal() {
	// Inject a fake executor that returns valid JSON nodes.
	runFn := func(ctx context.Context, actions ...chromedp.Action) error {
		for _, a := range actions {
			return a.Do(ctx)
		}
		return nil
	}
	exec := func(_ context.Context, _ string, _, res any) error {
		raw := res.(*struct {
			Nodes []json.RawMessage `json:"nodes"`
		})
		raw.Nodes = []json.RawMessage{
			// A simple node that strict unmarshal can handle.
			json.RawMessage(`{"nodeId":"n1","role":{"type":"role","value":"button"},"name":{"type":"string","value":"OK"},"backendDOMNodeId":1}`),
		}
		return nil
	}
	axFn := makeAxTreeFuncWith(runFn, exec)
	nodes, err := axFn(context.Background())
	require.NoError(s.T(), err)
	require.Len(s.T(), nodes, 1)
}

func (s *CDPSuite) TestMakeAxTreeFuncFallbackUnmarshal() {
	runFn := func(ctx context.Context, actions ...chromedp.Action) error {
		for _, a := range actions {
			return a.Do(ctx)
		}
		return nil
	}
	exec := func(_ context.Context, _ string, _, res any) error {
		raw := res.(*struct {
			Nodes []json.RawMessage `json:"nodes"`
		})
		raw.Nodes = []json.RawMessage{
			// A node with an unknown enum that causes strict unmarshal to fail,
			// but fallback parsing should succeed.
			json.RawMessage(`{"nodeId":"n1","role":{"type":"role","value":"button"},"name":{"type":"string","value":"Submit"},"description":{"type":"string","value":"desc"},"value":{"type":"string","value":"val"},"backendDOMNodeId":42,"ignoredReasons":[{"name":"uninteresting"}]}`),
		}
		return nil
	}
	axFn := makeAxTreeFuncWith(runFn, exec)
	nodes, err := axFn(context.Background())
	require.NoError(s.T(), err)
	require.Len(s.T(), nodes, 1)
	require.Equal(s.T(), cdp.BackendNodeID(42), nodes[0].BackendDOMNodeID)
}

func (s *CDPSuite) TestMakeAxTreeFuncFallbackBadJSON() {
	runFn := func(ctx context.Context, actions ...chromedp.Action) error {
		for _, a := range actions {
			return a.Do(ctx)
		}
		return nil
	}
	exec := func(_ context.Context, _ string, _, res any) error {
		raw := res.(*struct {
			Nodes []json.RawMessage `json:"nodes"`
		})
		raw.Nodes = []json.RawMessage{
			json.RawMessage(`{totally broken`),
		}
		return nil
	}
	axFn := makeAxTreeFuncWith(runFn, exec)
	nodes, err := axFn(context.Background())
	require.NoError(s.T(), err)
	require.Empty(s.T(), nodes) // bad JSON skipped
}

// --- EnableNetworkCapture ---

func (s *CDPSuite) TestEnableNetworkCaptureSuccess() {
	ch := make(chan NetworkRequest, 10)
	var capturedFn func(any)
	s.client.listenFunc = func(_ context.Context, fn func(any)) {
		capturedFn = fn
	}
	err := s.client.EnableNetworkCapture(context.Background(), ch)
	require.NoError(s.T(), err)
	require.NotNil(s.T(), capturedFn)

	// Simulate network request event.
	capturedFn(&network.EventRequestWillBeSent{
		RequestID: "req1",
		Request:   &network.Request{URL: "https://example.com/api", Method: "GET"},
		Type:      network.ResourceTypeXHR,
	})
	// Simulate response for the same request.
	capturedFn(&network.EventResponseReceived{
		RequestID: "req1",
		Response:  &network.Response{Status: 200, StatusText: "OK"},
		Type:      network.ResourceTypeXHR,
	})
	// Simulate response for unknown request (should be ignored).
	capturedFn(&network.EventResponseReceived{
		RequestID: "unknown",
		Response:  &network.Response{Status: 404, StatusText: "Not Found"},
		Type:      network.ResourceTypeDocument,
	})
	// Non-network event — should be ignored.
	capturedFn("not a network event")

	require.Len(s.T(), ch, 1)
	req := <-ch
	require.Equal(s.T(), "https://example.com/api", req.URL)
	require.Equal(s.T(), "GET", req.Method)
	require.Equal(s.T(), int64(200), req.Status)
	require.Equal(s.T(), "OK", req.StatusText)
}

func (s *CDPSuite) TestEnableNetworkCaptureRunError() {
	ch := make(chan NetworkRequest, 10)
	s.setRunFn(func(_ context.Context, _ ...chromedp.Action) error {
		return errors.New("network enable failed")
	})
	err := s.client.EnableNetworkCapture(context.Background(), ch)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "enabling network domain")
}

// --- ResizeWindow ---

func (s *CDPSuite) TestResizeWindowSuccess() {
	require.NoError(s.T(), s.client.ResizeWindow(context.Background(), 1024, 768))
}

func (s *CDPSuite) TestResizeWindowError() {
	s.setRunFn(func(_ context.Context, _ ...chromedp.Action) error { return errors.New("fail") })
	require.Error(s.T(), s.client.ResizeWindow(context.Background(), 1024, 768))
}

// --- ScrollIntoView ---

func (s *CDPSuite) TestScrollIntoViewSuccess() {
	require.NoError(s.T(), s.client.ScrollIntoView(context.Background(), cdp.BackendNodeID(42)))
}

func (s *CDPSuite) TestScrollIntoViewError() {
	s.setRunFn(func(_ context.Context, _ ...chromedp.Action) error { return errors.New("fail") })
	require.Error(s.T(), s.client.ScrollIntoView(context.Background(), cdp.BackendNodeID(42)))
}

// --- MouseDown ---

func (s *CDPSuite) TestMouseDownLeft() {
	require.NoError(s.T(), s.client.MouseDown(context.Background(), 100, 200, "left"))
}

func (s *CDPSuite) TestMouseDownRight() {
	require.NoError(s.T(), s.client.MouseDown(context.Background(), 100, 200, "right"))
}

func (s *CDPSuite) TestMouseDownMiddle() {
	require.NoError(s.T(), s.client.MouseDown(context.Background(), 100, 200, "middle"))
}

func (s *CDPSuite) TestMouseDownDefaultButton() {
	require.NoError(s.T(), s.client.MouseDown(context.Background(), 100, 200, ""))
}

func (s *CDPSuite) TestMouseDownError() {
	s.setRunFn(func(_ context.Context, _ ...chromedp.Action) error { return errors.New("fail") })
	require.Error(s.T(), s.client.MouseDown(context.Background(), 100, 200, "left"))
}

// --- MouseUp ---

func (s *CDPSuite) TestMouseUpLeft() {
	require.NoError(s.T(), s.client.MouseUp(context.Background(), 100, 200, "left"))
}

func (s *CDPSuite) TestMouseUpRight() {
	require.NoError(s.T(), s.client.MouseUp(context.Background(), 100, 200, "right"))
}

func (s *CDPSuite) TestMouseUpMiddle() {
	require.NoError(s.T(), s.client.MouseUp(context.Background(), 100, 200, "middle"))
}

func (s *CDPSuite) TestMouseUpDefaultButton() {
	require.NoError(s.T(), s.client.MouseUp(context.Background(), 100, 200, ""))
}

func (s *CDPSuite) TestMouseUpError() {
	s.setRunFn(func(_ context.Context, _ ...chromedp.Action) error { return errors.New("fail") })
	require.Error(s.T(), s.client.MouseUp(context.Background(), 100, 200, "left"))
}

// --- SwitchTarget ---

func (s *CDPSuite) TestSwitchTargetSuccess() {
	// SwitchTarget uses Chrome's HTTP /json/activate endpoint.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Contains(s.T(), r.URL.Path, "/json/activate/new-target-id")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// Set wsURL to use the test server (convert http:// to ws:// for the URL format).
	s.client.wsURL = strings.Replace(srv.URL, "http://", "ws://", 1)

	err := s.client.SwitchTarget("new-target-id")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "new-target-id", s.client.TargetID())
}

func (s *CDPSuite) TestSwitchTargetHTTPError() {
	// wsURL points to unreachable host.
	s.client.wsURL = "ws://127.0.0.1:1"

	err := s.client.SwitchTarget("bad-target")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "activating target bad-target")
}

// --- ListTabs via HTTP ---

func (s *CDPSuite) TestListTabsHTTP() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(s.T(), "/json/list", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[{"id":"t1","type":"page","url":"https://a.com","title":"A"},{"id":"t2","type":"page","url":"https://b.com","title":"B"},{"id":"bg1","type":"background_page","url":"","title":""}]`)
	}))
	defer srv.Close()

	s.client.wsURL = strings.Replace(srv.URL, "http://", "ws://", 1)

	tabs, err := s.client.ListTabs(context.Background())
	require.NoError(s.T(), err)
	require.Len(s.T(), tabs, 2) // background_page filtered out
	// Ordering is now handled by the manager's OrderTabs, not by ListTabs.
	require.Equal(s.T(), "t1", tabs[0].TargetID)
	require.Equal(s.T(), "A", tabs[0].Title)
	require.Equal(s.T(), "t2", tabs[1].TargetID)
}

func (s *CDPSuite) TestListTabsHTTPErrorFallback() {
	// HTTP fails but CDP fallback succeeds with real targets.
	s.client.wsURL = "ws://127.0.0.1:1"
	s.client.targetsFunc = func(_ context.Context) ([]*target.Info, error) {
		return []*target.Info{
			{TargetID: "t1", Type: "page", URL: "https://a.com", Title: "A"},
			{TargetID: "bg", Type: "background_page"},
		}, nil
	}
	tabs, err := s.client.ListTabs(context.Background())
	require.NoError(s.T(), err)
	require.Len(s.T(), tabs, 1)
	require.Equal(s.T(), "t1", tabs[0].TargetID)
}

func (s *CDPSuite) TestListTabsHTTPErrorBothFail() {
	s.client.wsURL = "ws://127.0.0.1:1"
	s.client.targetsFunc = func(_ context.Context) ([]*target.Info, error) {
		return nil, errors.New("cdp also failed")
	}
	_, err := s.client.ListTabs(context.Background())
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "cdp also failed")
}

func (s *CDPSuite) TestListTabsHTTPBadJSONFallback() {
	// HTTP returns invalid JSON but CDP fallback succeeds with real targets.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "not json")
	}))
	defer srv.Close()
	s.client.wsURL = strings.Replace(srv.URL, "http://", "ws://", 1)
	s.client.targetsFunc = func(_ context.Context) ([]*target.Info, error) {
		return []*target.Info{
			{TargetID: "t1", Type: "page", URL: "https://a.com", Title: "A"},
		}, nil
	}

	tabs, err := s.client.ListTabs(context.Background())
	require.NoError(s.T(), err)
	require.Len(s.T(), tabs, 1)
}

func (s *CDPSuite) TestListTabsHTTPBadJSONBothFail() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "not json")
	}))
	defer srv.Close()
	s.client.wsURL = strings.Replace(srv.URL, "http://", "ws://", 1)
	s.client.targetsFunc = func(_ context.Context) ([]*target.Info, error) {
		return nil, errors.New("cdp also failed")
	}

	_, err := s.client.ListTabs(context.Background())
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "decoding targets")
}

// --- ResetScreencast ---

func (s *CDPSuite) TestResetScreencast() {
	s.client.screencasting = true
	s.client.ResetScreencast()
	require.False(s.T(), s.client.screencasting)
}

func (s *CDPSuite) TestResetScreencastWhenNotScreencasting() {
	s.client.screencasting = false
	s.client.ResetScreencast()
	require.False(s.T(), s.client.screencasting)
}

func (s *CDPSuite) TestChromeHTTPBaseURL() {
	require.Equal(s.T(), "http://127.0.0.1:9222", ChromeHTTPBaseURL("ws://127.0.0.1:9222"))
	require.Equal(s.T(), "http://localhost:55008", ChromeHTTPBaseURL("ws://localhost:55008"))
}

func (s *CDPSuite) TestActivateTargetSuccess() {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(s.T(), "/json/activate/target-123", r.URL.Path)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()
	wsURL := strings.Replace(ts.URL, "http://", "ws://", 1)
	require.NoError(s.T(), ActivateTarget(wsURL, "target-123"))
}

func (s *CDPSuite) TestActivateTargetError() {
	err := ActivateTarget("ws://127.0.0.1:1", "target-x")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "activating target target-x")
}

func (s *CDPSuite) TestCreatePageTargetSuccess() {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(s.T(), http.MethodPut, r.Method)
		require.Equal(s.T(), "/json/new", r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "new-target-42"})
	}))
	defer ts.Close()
	wsURL := strings.Replace(ts.URL, "http://", "ws://", 1)
	id, err := CreatePageTarget(wsURL)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "new-target-42", id)
}

func (s *CDPSuite) TestCreatePageTargetNetworkError() {
	_, err := CreatePageTarget("ws://127.0.0.1:1")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating new page target")
}

func (s *CDPSuite) TestCreatePageTargetEmptyID() {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"id": ""})
	}))
	defer ts.Close()
	wsURL := strings.Replace(ts.URL, "http://", "ws://", 1)
	_, err := CreatePageTarget(wsURL)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "empty target ID")
}

func (s *CDPSuite) TestCreatePageTargetBadJSON() {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer ts.Close()
	wsURL := strings.Replace(ts.URL, "http://", "ws://", 1)
	_, err := CreatePageTarget(wsURL)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "decoding new target")
}
