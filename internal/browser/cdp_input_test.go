package browser

import (
	"context"
	"errors"
	"fmt"

	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

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
	require.NoError(s.T(), s.client.MouseMove(context.Background(), 50, 60, 0))
}

func (s *CDPSuite) TestMouseMoveWithButtons() {
	require.NoError(s.T(), s.client.MouseMove(context.Background(), 50, 60, 1))
}

func (s *CDPSuite) TestMouseMoveError() {
	s.setRunFn(func(_ context.Context, _ ...chromedp.Action) error { return errors.New("fail") })
	require.Error(s.T(), s.client.MouseMove(context.Background(), 50, 60, 0))
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
	var closedID string
	s.client.closeTabFunc = func(_ context.Context, tid string) error {
		closedID = tid
		return nil
	}
	err := s.client.CloseTab(context.Background(), "target-123")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "target-123", closedID)
}

func (s *CDPSuite) TestCloseTabError() {
	s.client.closeTabFunc = func(_ context.Context, _ string) error {
		return fmt.Errorf("close failed")
	}
	err := s.client.CloseTab(context.Background(), "t1")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "close failed")
}
