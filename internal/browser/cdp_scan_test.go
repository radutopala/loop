package browser

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/cdp"
	cdpdom "github.com/chromedp/cdproto/dom"
	"github.com/chromedp/chromedp"
	"github.com/go-json-experiment/json/jsontext"
	"github.com/stretchr/testify/require"
)

// scanExecResult is the shape makeScanRefsFuncWith decodes Runtime.evaluate
// into. Tests fill it through the injected executor.
type scanExecResult = struct {
	Result struct {
		Value json.RawMessage `json:"value"`
	} `json:"result"`
	ExceptionDetails *struct {
		Text string `json:"text"`
	} `json:"exceptionDetails"`
}

// directRun runs the action it is handed, so an injected executor is reached.
func directRun(ctx context.Context, actions ...chromedp.Action) error {
	for _, a := range actions {
		return a.Do(ctx)
	}
	return nil
}

// --- elementRefs: scan first, accessibility walk as the fallback ---

func (s *CDPSuite) TestGetElementRefsUsesTheInPageScan() {
	s.client.scanRefsFunc = func(_ context.Context) ([]ElementRef, error) {
		return []ElementRef{
			{Role: "button", Name: "Submit", X: 10, Y: 20, Width: 100, Height: 50},
			{Role: "link", Name: "Home", X: 0, Y: 0, Width: 30, Height: 10},
		}, nil
	}
	// The walk would answer too, with something else, so a wrong result here
	// is visible rather than merely absent.
	s.client.axTreeFunc = func(_ context.Context) ([]*accessibility.Node, error) {
		return []*accessibility.Node{{
			Role:             &accessibility.Value{Value: jsontext.Value("button")},
			BackendDOMNodeID: cdp.BackendNodeID(1),
		}}, nil
	}

	refs, err := s.client.GetElementRefs(context.Background())
	require.NoError(s.T(), err)
	require.Len(s.T(), refs, 2)
	require.Equal(s.T(), "ref_1", refs[0].RefID)
	require.Equal(s.T(), "Submit", refs[0].Name)
	require.Equal(s.T(), "ref_2", refs[1].RefID)
	require.Equal(s.T(), "Home", refs[1].Name)
}

// A scan that fails or sees nothing is the cross-origin-frame case, and the
// document that has stopped answering script at all: the walk is the only
// remaining way to describe the page.
func (s *CDPSuite) TestGetElementRefsFallsBackToTheAccessibilityWalk() {
	cases := []struct {
		name string
		scan func(context.Context) ([]ElementRef, error)
	}{
		{"scan failed", func(_ context.Context) ([]ElementRef, error) {
			return nil, errors.New("evaluate refused")
		}},
		{"scan saw nothing", func(_ context.Context) ([]ElementRef, error) {
			return nil, nil
		}},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			s.client.scanRefsFunc = tc.scan
			s.client.axTreeFunc = func(_ context.Context) ([]*accessibility.Node, error) {
				return []*accessibility.Node{{
					Role:             &accessibility.Value{Value: jsontext.Value("button")},
					Name:             &accessibility.Value{Value: jsontext.Value("From the walk")},
					BackendDOMNodeID: cdp.BackendNodeID(1),
				}}, nil
			}
			s.client.boxModelFunc = func(_ context.Context, _ cdp.BackendNodeID) (*cdpdom.BoxModel, error) {
				return &cdpdom.BoxModel{Content: []float64{0, 0, 100, 0, 100, 50, 0, 50}}, nil
			}

			refs, err := s.client.GetElementRefs(context.Background())
			require.NoError(s.T(), err)
			require.Len(s.T(), refs, 1)
			require.Equal(s.T(), "From the walk", refs[0].Name)
		})
	}
}

// --- makeScanRefsFunc ---

func (s *CDPSuite) TestMakeScanRefsFuncDecodesTheScan() {
	exec := func(_ context.Context, method string, _, res any) error {
		require.Equal(s.T(), "Runtime.evaluate", method)
		out := res.(*scanExecResult)
		out.Result.Value = json.RawMessage(
			`[{"role":"button","name":"Submit","description":"d","value":"v","x":1,"y":2,"width":3,"height":4}]`)
		return nil
	}

	refs, err := makeScanRefsFuncWith(directRun, exec)(context.Background())
	require.NoError(s.T(), err)
	require.Len(s.T(), refs, 1)
	require.Equal(s.T(), ElementRef{
		Role: "button", Name: "Submit", Description: "d", Value: "v",
		X: 1, Y: 2, Width: 3, Height: 4,
	}, refs[0])
}

func (s *CDPSuite) TestMakeScanRefsFuncErrors() {
	cases := []struct {
		name  string
		runFn func(context.Context, ...chromedp.Action) error
		exec  cdpExecutor
		wants string
	}{
		{
			name:  "run refused",
			runFn: func(_ context.Context, _ ...chromedp.Action) error { return errors.New("run failed") },
			exec:  func(_ context.Context, _ string, _, _ any) error { return nil },
			wants: "run failed",
		},
		{
			name:  "evaluate refused",
			runFn: directRun,
			exec:  func(_ context.Context, _ string, _, _ any) error { return errors.New("evaluate failed") },
			wants: "evaluate failed",
		},
		{
			name:  "the page threw",
			runFn: directRun,
			exec: func(_ context.Context, _ string, _, res any) error {
				out := res.(*scanExecResult)
				out.ExceptionDetails = &struct {
					Text string `json:"text"`
				}{Text: "Uncaught TypeError"}
				return nil
			},
			wants: "Uncaught TypeError",
		},
		{
			name:  "the page answered with something else",
			runFn: directRun,
			exec: func(_ context.Context, _ string, _, res any) error {
				res.(*scanExecResult).Result.Value = json.RawMessage(`"not an array"`)
				return nil
			},
			wants: "cannot unmarshal",
		},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			refs, err := makeScanRefsFuncWith(tc.runFn, tc.exec)(context.Background())
			require.Error(s.T(), err)
			require.Contains(s.T(), err.Error(), tc.wants)
			require.Nil(s.T(), refs)
		})
	}
}

// A page with nothing to click answers with an empty array, and one that
// answers with no value at all — Runtime.evaluate on a document mid-teardown —
// is the same nothing rather than a decode error.
func (s *CDPSuite) TestMakeScanRefsFuncEmptyAnswers() {
	cases := []struct {
		name  string
		value json.RawMessage
	}{
		{"empty array", json.RawMessage(`[]`)},
		{"no value", nil},
	}

	for _, tc := range cases {
		s.Run(tc.name, func() {
			exec := func(_ context.Context, _ string, _, res any) error {
				res.(*scanExecResult).Result.Value = tc.value
				return nil
			}
			refs, err := makeScanRefsFuncWith(directRun, exec)(context.Background())
			require.NoError(s.T(), err)
			require.Empty(s.T(), refs)
		})
	}
}

func (s *CDPSuite) TestMakeScanRefsFuncUsesRealExecutor() {
	// makeScanRefsFunc is the production wiring: cdp.Execute against a
	// context with no CDP connection, which fails rather than hanging.
	_, err := makeScanRefsFunc(directRun)(context.Background())
	require.Error(s.T(), err)
}

// --- the scan expression ---

// The scan and the walk have to agree on what counts as interactive, or a
// page's refs would change shape depending on which path answered.
func TestScanRefsJSCarriesEveryInteractiveRole(t *testing.T) {
	for role := range interactiveRoles {
		require.Contains(t, scanRoleList, `"`+role+`"`, "role %q missing from the scan", role)
	}
	require.Contains(t, scanRefsJS, scanRoleList)
	require.Contains(t, scanRefsJS, "5000", "the ref budget should reach the page")
	require.NotContains(t, scanRefsJS, "%!", "the template should be fully substituted")
	require.True(t, strings.HasPrefix(scanRefsJS, "(() => {"), "the scan should be one expression")
}
