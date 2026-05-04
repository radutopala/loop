package browser

import (
	"context"
	"errors"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/cdp"
	cdpdom "github.com/chromedp/cdproto/dom"
	"github.com/chromedp/chromedp"
	"github.com/go-json-experiment/json/jsontext"
	"github.com/stretchr/testify/require"
)

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
