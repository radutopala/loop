package browser

import (
	"context"
	"encoding/base64"
	"errors"

	cdpdom "github.com/chromedp/cdproto/dom"
	cdppage "github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

// runActions makes runFn run each action against the injected exec.
func (s *CDPSuite) runActions() {
	s.setRunFn(func(ctx context.Context, actions ...chromedp.Action) error {
		for _, a := range actions {
			if err := a.Do(ctx); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *CDPSuite) TestFullPageScreenshot() {
	png := []byte("png-bytes")
	tests := []struct {
		name       string
		maxHeight  int
		pageHeight float64
		wantClip   float64
		wantHeight int
	}{
		{"short page", 8000, 1200.4, 1201, 1201},
		{"cut at max", 8000, 20000, 8000, 20000},
		{"zero max uses the limit", 0, 30000, MaxFullPageHeight, 30000},
		{"max above the limit", 50000, 30000, MaxFullPageHeight, 30000},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			var shot *cdppage.CaptureScreenshotParams
			s.client.exec = func(_ context.Context, m string, p, res any) error {
				switch m {
				case cdppage.CommandGetLayoutMetrics:
					res.(*cdppage.GetLayoutMetricsReturns).CSSContentSize = &cdpdom.Rect{Width: 1279.5, Height: tt.pageHeight}
				case cdppage.CommandCaptureScreenshot:
					shot = p.(*cdppage.CaptureScreenshotParams)
					res.(*cdppage.CaptureScreenshotReturns).Data = base64.StdEncoding.EncodeToString(png)
				}
				return nil
			}
			s.runActions()
			data, height, err := s.client.FullPageScreenshot(context.Background(), tt.maxHeight)
			require.NoError(s.T(), err)
			require.Equal(s.T(), png, data)
			require.Equal(s.T(), tt.wantHeight, height)
			require.Equal(s.T(), &cdppage.Viewport{Width: 1280, Height: tt.wantClip, Scale: 1}, shot.Clip)
			require.True(s.T(), shot.CaptureBeyondViewport)
			require.Equal(s.T(), cdppage.CaptureScreenshotFormatPng, shot.Format)
		})
	}
}

func (s *CDPSuite) TestFullPageScreenshotErrors() {
	measured := func(res any) {
		res.(*cdppage.GetLayoutMetricsReturns).CSSContentSize = &cdpdom.Rect{Width: 800, Height: 600}
	}
	tests := []struct {
		name string
		exec cdpExecutor
		want string
	}{
		{"metrics fail", func(context.Context, string, any, any) error { return errors.New("boom") }, "measuring page: boom"},
		{"no content size", func(context.Context, string, any, any) error { return nil }, "measuring page: no content size"},
		{"capture fails", func(_ context.Context, m string, _, res any) error {
			if m == cdppage.CommandCaptureScreenshot {
				return errors.New("too big")
			}
			measured(res)
			return nil
		}, "capturing screenshot: too big"},
		{"bad data", func(_ context.Context, m string, _, res any) error {
			if m == cdppage.CommandCaptureScreenshot {
				res.(*cdppage.CaptureScreenshotReturns).Data = "!!!"
				return nil
			}
			measured(res)
			return nil
		}, "decoding screenshot"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.client.exec = tt.exec
			s.runActions()
			data, height, err := s.client.FullPageScreenshot(context.Background(), 8000)
			require.ErrorContains(s.T(), err, tt.want)
			require.Nil(s.T(), data)
			require.Zero(s.T(), height)
		})
	}
}
