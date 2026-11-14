package browser

import (
	"context"
	"errors"

	cdpbrowser "github.com/chromedp/cdproto/browser"
	cdppage "github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

func (s *CDPSuite) TestWatchTargets() {
	var listener func(any)
	s.client.listenFunc = func(_ context.Context, fn func(any)) { listener = fn }

	type opened struct {
		tab    TabInfo
		opener string
	}
	var gotOpened []opened
	var gotClosed []string
	s.client.WatchTargets(
		func(tab TabInfo, openerID string) { gotOpened = append(gotOpened, opened{tab, openerID}) },
		func(targetID string) { gotClosed = append(gotClosed, targetID) },
	)
	require.NotNil(s.T(), listener)

	for _, ev := range []any{
		&target.EventTargetCreated{TargetInfo: &target.Info{TargetID: "popup", Type: "page", URL: "https://b.com", Title: "B", OpenerID: "t1"}},
		&target.EventTargetCreated{TargetInfo: &target.Info{TargetID: "sw", Type: "service_worker"}},
		&target.EventTargetCreated{},
		&target.EventTargetDestroyed{TargetID: "t1"},
		&target.EventTargetInfoChanged{TargetInfo: &target.Info{TargetID: "t1", Type: "page"}},
	} {
		listener(ev)
	}

	require.Equal(s.T(), []opened{{TabInfo{TargetID: "popup", URL: "https://b.com", Title: "B"}, "t1"}}, gotOpened)
	require.Equal(s.T(), []string{"t1"}, gotClosed)
}

func (s *CDPSuite) TestWatchDialogs() {
	var listener func(any)
	s.client.listenFunc = func(_ context.Context, fn func(any)) { listener = fn }
	s.client.targetID = target.ID("t1")

	var gotOpened []Dialog
	var gotClosed []string
	s.client.WatchDialogs(
		func(d Dialog) { gotOpened = append(gotOpened, d) },
		func(targetID string) { gotClosed = append(gotClosed, targetID) },
	)
	require.NotNil(s.T(), listener)

	// The tab is the one the client was on when it started watching: the
	// events come from that session whatever it is pointed at later.
	s.client.targetID = target.ID("t2")
	for _, ev := range []any{
		&cdppage.EventJavascriptDialogOpening{Type: cdppage.DialogTypePrompt, Message: "Name?", DefaultPrompt: "bob", URL: "https://a.com"},
		&cdppage.EventJavascriptDialogClosed{Result: true},
		&cdppage.EventLoadEventFired{},
	} {
		listener(ev)
	}

	require.Equal(s.T(), []Dialog{{TargetID: "t1", Type: "prompt", Message: "Name?", DefaultPrompt: "bob", URL: "https://a.com"}}, gotOpened)
	require.Equal(s.T(), []string{"t1"}, gotClosed)
}

func (s *CDPSuite) TestHandleDialog() {
	var method string
	var params any
	s.client.exec = func(_ context.Context, m string, p, _ any) error {
		method, params = m, p
		return nil
	}
	s.setRunFn(func(ctx context.Context, actions ...chromedp.Action) error {
		for _, a := range actions {
			if err := a.Do(ctx); err != nil {
				return err
			}
		}
		return nil
	})
	require.NoError(s.T(), s.client.HandleDialog(context.Background(), true, "alice"))
	require.Equal(s.T(), cdppage.CommandHandleJavaScriptDialog, method)
	require.Equal(s.T(), cdppage.HandleJavaScriptDialog(true).WithPromptText("alice"), params)
}

func (s *CDPSuite) TestHandleDialogError() {
	s.setRunFn(func(context.Context, ...chromedp.Action) error { return errors.New("no dialog is showing") })
	require.ErrorContains(s.T(), s.client.HandleDialog(context.Background(), false, ""), "no dialog is showing")
}

func (s *CDPSuite) TestWatchDownloads() {
	var listener func(any)
	s.client.listenBrowserFunc = func(_ context.Context, fn func(any)) { listener = fn }

	var got []Download
	s.client.WatchDownloads(func(d Download) { got = append(got, d) })
	require.NotNil(s.T(), listener)

	for _, ev := range []any{
		&cdpbrowser.EventDownloadWillBegin{GUID: "g1", URL: "https://a.com/r.pdf", SuggestedFilename: "r.pdf"},
		&cdpbrowser.EventDownloadProgress{GUID: "g1", State: cdpbrowser.DownloadProgressStateInProgress},
		&cdpbrowser.EventDownloadProgress{GUID: "g1", State: cdpbrowser.DownloadProgressStateCompleted, FilePath: "/downloads/r (1).pdf"},
		&cdpbrowser.EventDownloadProgress{GUID: "g2", State: cdpbrowser.DownloadProgressStateCompleted},
		&cdpbrowser.EventDownloadProgress{GUID: "g3", State: cdpbrowser.DownloadProgressStateCanceled},
		&target.EventTargetDestroyed{TargetID: "t1"},
	} {
		listener(ev)
	}

	require.Equal(s.T(), []Download{
		{GUID: "g1", URL: "https://a.com/r.pdf", Filename: "r.pdf", State: DownloadStarted},
		{GUID: "g1", Filename: "r (1).pdf", State: DownloadCompleted},
		{GUID: "g2", State: DownloadCompleted},
		{GUID: "g3", State: DownloadCanceled},
	}, got)
}
