package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"testing"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/cdp"
	cdpdom "github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/network"
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

// --- NewCDPClient with CDP-based target discovery ---

func (s *CDPSuite) TestNewCDPClientDiscoveryFallback() {
	// When CDP target discovery fails (e.g. no real Chrome), NewCDPClient
	// should still succeed — it just skips the WithTargetID option.
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
	// WithNewTarget sets reuseTarget=false so CDP discovery creates a new target.
	// Without a real Chrome, the discovery fails silently and resolvedTargetID stays empty.
	c, err := NewCDPClient(context.Background(), "ws://test:9222", slog.Default(),
		WithAllocator(func(parent context.Context, _ string) (context.Context, context.CancelFunc) {
			return context.WithCancel(parent)
		}),
		WithRunFunc(func(_ context.Context, _ ...chromedp.Action) error { return nil }),
		WithNewTarget(),
	)
	require.NoError(s.T(), err)
	require.NotNil(s.T(), c)
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

	// Exercise closeTabFunc (default closure via chromedp.NewContext + cfg.runFunc).
	_ = c.closeTabFunc(context.Background(), "some-target")

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
			// A node that fails cdproto's strict unmarshal but succeeds via the
			// lenient fallback. childIds carries the wrong TYPE (number, not
			// array) so the failure is version-independent — enum-based fixtures
			// (e.g. ignoredReasons "uninteresting") stop failing once cdproto
			// learns the value, silently skipping the fallback path.
			json.RawMessage(`{"nodeId":"n1","childIds":123,"role":{"type":"role","value":"button"},"name":{"type":"string","value":"Submit"},"description":{"type":"string","value":"desc"},"value":{"type":"string","value":"val"},"backendDOMNodeId":42}`),
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
	var activatedID target.ID
	s.client.activateFunc = func(_ context.Context, id target.ID) error {
		activatedID = id
		return nil
	}
	err := s.client.SwitchTarget("new-target-id")
	require.NoError(s.T(), err)
	require.Equal(s.T(), target.ID("new-target-id"), activatedID)
	require.Equal(s.T(), "new-target-id", s.client.TargetID())
}

func (s *CDPSuite) TestSwitchTargetError() {
	s.client.activateFunc = func(_ context.Context, _ target.ID) error {
		return fmt.Errorf("activate failed")
	}
	err := s.client.SwitchTarget("bad-target")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "activate failed")
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

// --- NewContextForTarget ---

func (s *CDPSuite) TestNewContextForTargetSuccess() {
	// Create a CDPClient via NewCDPClient to get a valid chromedp context.
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

	newClient, err := c.NewContextForTarget("new-target-id")
	require.NoError(s.T(), err)
	require.NotNil(s.T(), newClient)
	require.Equal(s.T(), "new-target-id", newClient.TargetID())

	// Exercise the closeTabFunc closure defined inside NewContextForTarget.
	_ = newClient.CloseTab(context.Background(), "some-target")

	newClient.Close()
}

func (s *CDPSuite) TestNewContextForTargetRunError() {
	callCount := 0
	c, err := NewCDPClient(context.Background(), "ws://test:9222", slog.Default(),
		WithAllocator(func(parent context.Context, _ string) (context.Context, context.CancelFunc) {
			return context.WithCancel(parent)
		}),
		WithRunFunc(func(_ context.Context, _ ...chromedp.Action) error {
			callCount++
			if callCount > 1 {
				return errors.New("attach failed")
			}
			return nil
		}),
	)
	require.NoError(s.T(), err)
	defer c.Close()

	newClient, err := c.NewContextForTarget("bad-target")
	require.Error(s.T(), err)
	require.Nil(s.T(), newClient)
	require.Contains(s.T(), err.Error(), "attaching to target bad-target")
}

// --- NewCDPClient with no target ID (resolvedTargetID == "" after run) ---

func (s *CDPSuite) TestNewCDPClientNoTargetIDResolution() {
	// No targetID set, fromContext returns nil. resolvedTargetID stays "".
	c, err := NewCDPClient(context.Background(), "ws://test:9222", slog.Default(),
		WithAllocator(func(parent context.Context, _ string) (context.Context, context.CancelFunc) {
			return context.WithCancel(parent)
		}),
		WithRunFunc(func(_ context.Context, _ ...chromedp.Action) error { return nil }),
	)
	require.NoError(s.T(), err)
	require.NotNil(s.T(), c)
	require.Equal(s.T(), "", c.TargetID())
	c.Close()
}

func (s *CDPSuite) TestNewCDPClientTargetIDFromContext() {
	// No targetID set, but fromContextFunc returns a target — resolvedTargetID gets set.
	c, err := NewCDPClient(context.Background(), "ws://test:9222", slog.Default(),
		WithAllocator(func(parent context.Context, _ string) (context.Context, context.CancelFunc) {
			return context.WithCancel(parent)
		}),
		WithRunFunc(func(_ context.Context, _ ...chromedp.Action) error { return nil }),
		func(cfg *cdpConfig) {
			cfg.fromContextFunc = func(_ context.Context) *chromedp.Context {
				return &chromedp.Context{
					Target: &chromedp.Target{
						TargetID: "auto-target-42",
					},
				}
			}
		},
	)
	require.NoError(s.T(), err)
	require.NotNil(s.T(), c)
	require.Equal(s.T(), "auto-target-42", c.TargetID())
	c.Close()
}
