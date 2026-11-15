package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/chromedp/cdproto/cdp"
	cdpdom "github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/page"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

// frameExec answers CDP calls by method, round-tripping each answer through
// JSON as Chrome's would arrive.
type frameExec map[string]func(params any) (any, error)

func (f frameExec) exec(_ context.Context, method string, params, res any) error {
	answer, ok := f[method]
	if !ok {
		return fmt.Errorf("unexpected %s", method)
	}
	v, err := answer(params)
	if err != nil || v == nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, res)
}

// attachingRun runs actions as chromedp would, attaching a frame context it
// is handed first, the way chromedp's first Run on a target context does.
func attachingRun(ctx context.Context, actions ...chromedp.Action) error {
	if c := chromedp.FromContext(ctx); c != nil && c.Target == nil {
		c.Target = &chromedp.Target{SessionID: "frame-session"}
	}
	return runActions(ctx, actions...)
}

// scanned is a Runtime.evaluate answer holding refs.
func scanned(refs ...ElementRef) map[string]any {
	return map[string]any{"result": map[string]any{"value": refs}}
}

// iframeBox is an iframe's box model at (x, y), 400 by 150.
func iframeBox(x, y float64) *cdpdom.BoxModel {
	return &cdpdom.BoxModel{Content: []float64{x, y, x + 400, y, x + 400, y + 150, x, y + 150}}
}

// useFrames points the test client at a page whose frames f describes, with
// the iframes laid out by boxes, keyed by their backend node id.
func (s *CDPSuite) useFrames(f frameExec, targets []*target.Info, boxes map[cdp.BackendNodeID]*cdpdom.BoxModel) {
	s.client.targetID = "page-1"
	s.client.runFn = attachingRun
	s.client.exec = f.exec
	s.client.targetsFunc = func(_ context.Context) ([]*target.Info, error) { return targets, nil }
	s.client.boxModelFunc = func(_ context.Context, id cdp.BackendNodeID) (*cdpdom.BoxModel, error) {
		return boxes[id], nil
	}
}

// --- frameRefs ---

func (s *CDPSuite) TestFrameRefsReadsBothKindsOfCrossOriginFrame() {
	var detached []target.SessionID
	f := frameExec{
		"Page.getFrameTree": func(_ any) (any, error) {
			return json.RawMessage(`{"frameTree":{"frame":{"id":"main","adFrameStatus":{"adFrameType":"from a later Chrome"}},"childFrames":[
				{"frame":{"id":"same-origin"},"childFrames":[{"frame":{"id":"same-site"}}]},
				{"frame":{"id":"crashed"}}]}}`), nil
		},
		"Page.createIsolatedWorld": func(p any) (any, error) {
			params := p.(*page.CreateIsolatedWorldParams)
			require.Equal(s.T(), frameWorldName, params.WorldName)
			switch params.FrameID {
			case "same-origin":
				return page.CreateIsolatedWorldReturns{ExecutionContextID: 1}, nil
			case "same-site":
				return page.CreateIsolatedWorldReturns{ExecutionContextID: 2}, nil
			}
			return nil, errors.New("frame is gone")
		},
		"Runtime.evaluate": func(p any) (any, error) {
			params := p.(*cdpruntime.EvaluateParams)
			require.Equal(s.T(), frameScanJS, params.Expression)
			require.True(s.T(), params.ReturnByValue)
			switch params.ContextID {
			case 1:
				// The page's own scan walked this one.
				return map[string]any{"result": map[string]any{"value": nil}}, nil
			case 2:
				return scanned(ElementRef{Role: "button", Name: "Pay", X: 10, Y: 10, Width: 40, Height: 20}), nil
			}
			return scanned(
				ElementRef{Role: "textbox", Name: "Card number", X: 10, Y: 40, Width: 200, Height: 20},
				// Scrolled out of the frame: a click there would land beside it.
				ElementRef{Role: "button", Name: "Below the fold", X: 10, Y: 400, Width: 40, Height: 20},
			), nil
		},
		"DOM.getFrameOwner": func(p any) (any, error) {
			switch p.(*cdpdom.GetFrameOwnerParams).FrameID {
			case "same-site":
				return cdpdom.GetFrameOwnerReturns{BackendNodeID: 7}, nil
			case "cross-site":
				return cdpdom.GetFrameOwnerReturns{BackendNodeID: 8}, nil
			}
			return nil, errors.New("no owner")
		},
		"Target.detachFromTarget": func(p any) (any, error) {
			detached = append(detached, p.(*target.DetachFromTargetParams).SessionID)
			return nil, nil
		},
	}
	s.useFrames(f, []*target.Info{
		{TargetID: "page-1", Type: "page"},
		{TargetID: "cross-site", Type: "iframe", ParentID: "page-1"},
		{TargetID: "another-tabs-frame", Type: "iframe", ParentID: "page-2"},
		{TargetID: "worker", Type: "worker", ParentID: "page-1"},
	}, map[cdp.BackendNodeID]*cdpdom.BoxModel{7: iframeBox(100, 200), 8: iframeBox(600, 200)})

	refs := s.client.frameRefs()
	require.Equal(s.T(), []ElementRef{
		{Role: "button", Name: "Pay", X: 110, Y: 210, Width: 40, Height: 20},
		{Role: "textbox", Name: "Card number", X: 610, Y: 240, Width: 200, Height: 20},
	}, refs)
	require.Equal(s.T(), []target.SessionID{"frame-session"}, detached)
}

// Chrome answers nothing about frames it cannot list, and the page's own refs
// stand alone.
func (s *CDPSuite) TestFrameRefsWithoutFrameListings() {
	f := frameExec{"Page.getFrameTree": func(_ any) (any, error) { return nil, errors.New("tree refused") }}
	s.useFrames(f, nil, nil)
	s.client.targetsFunc = func(_ context.Context) ([]*target.Info, error) { return nil, errors.New("targets refused") }

	require.Empty(s.T(), s.client.frameRefs())
}

func (s *CDPSuite) TestFrameRefsSkipsAFrameTargetThatFails() {
	var detached int
	f := frameExec{
		"Page.getFrameTree": func(_ any) (any, error) { return json.RawMessage(`{"frameTree":{"frame":{"id":"main"}}}`), nil },
		"Runtime.evaluate":  func(_ any) (any, error) { return nil, errors.New("renderer gone") },
		"Target.detachFromTarget": func(_ any) (any, error) {
			detached++
			return nil, errors.New("already detached")
		},
	}
	s.useFrames(f, []*target.Info{{TargetID: "cross-site", Type: "iframe", ParentID: "page-1"}}, nil)

	require.Empty(s.T(), s.client.frameRefs())
	require.Equal(s.T(), 1, detached)
}

// A context chromedp never attached has no session to let go of.
func (s *CDPSuite) TestFrameRefsDetachesOnlyAnAttachedFrame() {
	f := frameExec{
		"Page.getFrameTree": func(_ any) (any, error) { return json.RawMessage(`{"frameTree":{"frame":{"id":"main"}}}`), nil },
		"Runtime.evaluate":  func(_ any) (any, error) { return scanned(), nil },
	}
	s.useFrames(f, []*target.Info{{TargetID: "cross-site", Type: "iframe", ParentID: "page-1"}}, nil)
	s.client.runFn = runActions

	require.Empty(s.T(), s.client.frameRefs())
}

func (s *CDPSuite) TestFrameRefsCapsTheFrameTargets() {
	var targets []*target.Info
	for i := range maxScannedFrames + 5 {
		targets = append(targets, &target.Info{TargetID: target.ID(fmt.Sprintf("ad-%d", i)), Type: "iframe", ParentID: "page-1"})
	}
	scans := 0
	f := frameExec{
		"Page.getFrameTree": func(_ any) (any, error) { return json.RawMessage(`{"frameTree":{"frame":{"id":"main"}}}`), nil },
		"Runtime.evaluate": func(_ any) (any, error) {
			scans++
			return scanned(), nil
		},
		"Target.detachFromTarget": func(_ any) (any, error) { return nil, nil },
	}
	s.useFrames(f, targets, nil)

	require.Empty(s.T(), s.client.frameRefs())
	require.Equal(s.T(), maxScannedFrames, scans)
}

// --- placeInPage ---

// Where a frame's iframe is cannot always be told; its refs are then left out
// rather than placed at the frame's own coordinates.
func (s *CDPSuite) TestPlaceInPageWithoutTheIframesBox() {
	inFrame := []ElementRef{{Role: "button", Name: "Pay", X: 10, Y: 10, Width: 40, Height: 20}}
	cases := []struct {
		name  string
		owner func(any) (any, error)
		box   func(context.Context, cdp.BackendNodeID) (*cdpdom.BoxModel, error)
	}{
		{
			name:  "no owner",
			owner: func(_ any) (any, error) { return nil, errors.New("no owner") },
		},
		{
			name:  "box refused",
			owner: func(_ any) (any, error) { return cdpdom.GetFrameOwnerReturns{BackendNodeID: 7}, nil },
			box: func(_ context.Context, _ cdp.BackendNodeID) (*cdpdom.BoxModel, error) {
				return nil, errors.New("not rendered")
			},
		},
		{
			name:  "no box",
			owner: func(_ any) (any, error) { return cdpdom.GetFrameOwnerReturns{BackendNodeID: 7}, nil },
			box:   func(_ context.Context, _ cdp.BackendNodeID) (*cdpdom.BoxModel, error) { return nil, nil },
		},
		{
			name:  "short quad",
			owner: func(_ any) (any, error) { return cdpdom.GetFrameOwnerReturns{BackendNodeID: 7}, nil },
			box: func(_ context.Context, _ cdp.BackendNodeID) (*cdpdom.BoxModel, error) {
				return &cdpdom.BoxModel{Content: []float64{1, 2}}, nil
			},
		},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			s.useFrames(frameExec{"DOM.getFrameOwner": tc.owner}, nil, nil)
			if tc.box != nil {
				s.client.boxModelFunc = tc.box
			}
			require.Empty(s.T(), s.client.placeInPage("frame", inFrame))
		})
	}
}

// --- elementRefs with frames ---

func (s *CDPSuite) TestGetElementRefsAppendsFrameRefs() {
	cases := []struct {
		name string
		page []ElementRef
		want int
	}{
		{name: "after the page's", page: []ElementRef{{Role: "button", Name: "Outer"}}, want: 2},
		{name: "a page that is all frame", page: nil, want: 1},
		{name: "within the cap", page: make([]ElementRef, maxScannedRefs), want: maxScannedRefs},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			f := frameExec{
				"Page.getFrameTree": func(_ any) (any, error) {
					return json.RawMessage(`{"frameTree":{"frame":{"id":"main"},"childFrames":[{"frame":{"id":"pay"}}]}}`), nil
				},
				"Page.createIsolatedWorld": func(_ any) (any, error) { return page.CreateIsolatedWorldReturns{ExecutionContextID: 3}, nil },
				"Runtime.evaluate": func(_ any) (any, error) {
					return scanned(ElementRef{Role: "button", Name: "Pay", X: 10, Y: 10, Width: 40, Height: 20}), nil
				},
				"DOM.getFrameOwner": func(_ any) (any, error) { return cdpdom.GetFrameOwnerReturns{BackendNodeID: 7}, nil },
			}
			s.useFrames(f, nil, map[cdp.BackendNodeID]*cdpdom.BoxModel{7: iframeBox(100, 200)})
			pageRefs := append([]ElementRef(nil), tc.page...)
			s.client.scanRefsFunc = func(_ context.Context) ([]ElementRef, error) { return pageRefs, nil }

			refs, err := s.client.GetElementRefs(context.Background())
			require.NoError(s.T(), err)
			require.Len(s.T(), refs, tc.want)
			last := refs[len(refs)-1]
			require.Equal(s.T(), fmt.Sprintf("ref_%d", tc.want), last.RefID)
			if tc.want < maxScannedRefs {
				require.Equal(s.T(), "Pay", last.Name)
				require.InDelta(s.T(), 110, last.X, 0)
			}
		})
	}
}
