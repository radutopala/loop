package browser

import (
	"context"
	"errors"
	"fmt"

	cdpbrowser "github.com/chromedp/cdproto/browser"
	cdpdom "github.com/chromedp/cdproto/dom"
	cdppage "github.com/chromedp/cdproto/page"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/go-json-experiment/json/jsontext"
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
	var gotChanged []TabInfo
	var gotClosed []string
	s.client.WatchTargets(
		func(tab TabInfo, openerID string) { gotOpened = append(gotOpened, opened{tab, openerID}) },
		func(tab TabInfo) { gotChanged = append(gotChanged, tab) },
		func(targetID string) { gotClosed = append(gotClosed, targetID) },
	)
	require.NotNil(s.T(), listener)

	for _, ev := range []any{
		&target.EventTargetCreated{TargetInfo: &target.Info{TargetID: "popup", Type: "page", URL: "https://b.com", Title: "B", OpenerID: "t1"}},
		&target.EventTargetCreated{TargetInfo: &target.Info{TargetID: "sw", Type: "service_worker"}},
		&target.EventTargetCreated{},
		&target.EventTargetDestroyed{TargetID: "t1"},
		&target.EventTargetInfoChanged{TargetInfo: &target.Info{TargetID: "t1", Type: "page", URL: "https://c.com", Title: "C"}},
		&target.EventTargetInfoChanged{TargetInfo: &target.Info{TargetID: "w1", Type: "worker"}},
		&target.EventTargetInfoChanged{},
	} {
		listener(ev)
	}

	require.Equal(s.T(), []opened{{TabInfo{TargetID: "popup", URL: "https://b.com", Title: "B"}, "t1"}}, gotOpened)
	require.Equal(s.T(), []TabInfo{{TargetID: "t1", URL: "https://c.com", Title: "C"}}, gotChanged)
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

func (s *CDPSuite) TestInterceptFileChoosers() {
	var method string
	var params any
	s.client.exec = func(_ context.Context, m string, p, _ any) error {
		method, params = m, p
		return nil
	}
	s.setRunFn(runActions)
	require.NoError(s.T(), s.client.InterceptFileChoosers(context.Background()))
	require.Equal(s.T(), cdppage.CommandSetInterceptFileChooserDialog, method)
	require.Equal(s.T(), cdppage.SetInterceptFileChooserDialog(true), params)
}

func (s *CDPSuite) TestWatchFileChoosers() {
	var listener func(any)
	s.client.listenFunc = func(_ context.Context, fn func(any)) { listener = fn }
	s.client.targetID = target.ID("t1")

	var got []FileChooser
	s.client.WatchFileChoosers(func(fc FileChooser) { got = append(got, fc) })
	require.NotNil(s.T(), listener)

	s.client.targetID = target.ID("t2")
	for _, ev := range []any{
		&cdppage.EventFileChooserOpened{Mode: cdppage.FileChooserOpenedModeSelectMultiple, BackendNodeID: 7},
		&cdppage.EventFileChooserOpened{Mode: cdppage.FileChooserOpenedModeSelectSingle, BackendNodeID: 8},
		// showOpenFilePicker: no input to put the files on.
		&cdppage.EventFileChooserOpened{Mode: cdppage.FileChooserOpenedModeSelectSingle},
		&cdppage.EventLoadEventFired{},
	} {
		listener(ev)
	}

	require.Equal(s.T(), []FileChooser{
		{TargetID: "t1", Multiple: true, NodeID: 7},
		{TargetID: "t1", NodeID: 8},
	}, got)
}

func (s *CDPSuite) TestSetChosenFiles() {
	files := []ChosenFile{{Name: "a.txt", Type: "text/plain", Data: "aGk="}}
	okResult := &cdpruntime.RemoteObject{Value: jsontext.Value(`"ok"`)}
	tests := []struct {
		name    string
		files   []ChosenFile
		node    *cdpruntime.RemoteObject
		resolve error
		call    error
		result  cdpruntime.CallFunctionOnReturns
		wantArg string
		wantErr string
	}{
		{name: "files set", files: files, node: &cdpruntime.RemoteObject{ObjectID: "obj"}, result: cdpruntime.CallFunctionOnReturns{Result: okResult},
			wantArg: `[{"name":"a.txt","type":"text/plain","data":"aGk="}]`},
		{name: "cancelled", node: &cdpruntime.RemoteObject{ObjectID: "obj"}, result: cdpruntime.CallFunctionOnReturns{Result: okResult}},
		{name: "node not found", files: files, resolve: errors.New("No node with given id found"), wantErr: "finding the file input: No node"},
		{name: "node resolves to nothing", files: files, wantErr: "the file input is gone"},
		{name: "call fails", files: files, node: &cdpruntime.RemoteObject{ObjectID: "obj"}, call: errors.New("context gone"), wantErr: "setting the files: context gone"},
		{name: "page throws", files: files, node: &cdpruntime.RemoteObject{ObjectID: "obj"},
			result:  cdpruntime.CallFunctionOnReturns{ExceptionDetails: &cdpruntime.ExceptionDetails{Text: "Uncaught", Exception: &cdpruntime.RemoteObject{Description: "InvalidCharacterError"}}},
			wantErr: "InvalidCharacterError"},
		{name: "page refuses", files: files, node: &cdpruntime.RemoteObject{ObjectID: "obj"},
			result:  cdpruntime.CallFunctionOnReturns{Result: &cdpruntime.RemoteObject{Value: jsontext.Value(`"the file input is gone"`)}},
			wantErr: "setting the files: the file input is gone"},
		{name: "no result", files: files, node: &cdpruntime.RemoteObject{ObjectID: "obj"}, wantErr: "setting the files: "},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			var call *cdpruntime.CallFunctionOnParams
			s.client.exec = func(_ context.Context, m string, p, res any) error {
				switch m {
				case cdpdom.CommandResolveNode:
					require.Equal(s.T(), cdpdom.ResolveNode().WithBackendNodeID(9), p)
					res.(*cdpdom.ResolveNodeReturns).Object = tc.node
					return tc.resolve
				case cdpruntime.CommandCallFunctionOn:
					call = p.(*cdpruntime.CallFunctionOnParams)
					*res.(*cdpruntime.CallFunctionOnReturns) = tc.result
					return tc.call
				}
				return fmt.Errorf("unexpected %s", m)
			}
			s.setRunFn(runActions)

			err := s.client.SetChosenFiles(context.Background(), 9, tc.files)
			if tc.wantErr != "" {
				require.ErrorContains(s.T(), err, tc.wantErr)
				return
			}
			require.NoError(s.T(), err)
			require.Equal(s.T(), setChosenFilesJS, call.FunctionDeclaration)
			require.Equal(s.T(), cdpruntime.RemoteObjectID("obj"), call.ObjectID)
			require.True(s.T(), call.ReturnByValue)
			if tc.wantArg == "" {
				require.Empty(s.T(), call.Arguments, "a cancel passes no files")
				return
			}
			require.Len(s.T(), call.Arguments, 1)
			require.JSONEq(s.T(), tc.wantArg, string(call.Arguments[0].Value))
		})
	}
}

// runActions runs each action against the context it is given, as chromedp
// would, so the CDP calls inside them reach the client's exec.
func runActions(ctx context.Context, actions ...chromedp.Action) error {
	for _, a := range actions {
		if err := a.Do(ctx); err != nil {
			return err
		}
	}
	return nil
}
