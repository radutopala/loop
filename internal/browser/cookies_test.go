package browser

import (
	"context"
	"errors"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/storage"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/require"
)

// cookieExecutor stands in for the CDP connection and records the command it
// was handed, so the field mapping can be asserted without a browser.
type cookieExecutor struct {
	method string
	params *storage.SetCookiesParams
	err    error
}

func (e *cookieExecutor) Execute(_ context.Context, method string, params, _ any) error {
	e.method = method
	if p, ok := params.(*storage.SetCookiesParams); ok {
		e.params = p
	}
	return e.err
}

// runWithExecutor drives the action the client passes to runFn against a
// fake CDP executor.
func (s *CDPSuite) runWithExecutor(exec *cookieExecutor) {
	s.setRunFn(func(ctx context.Context, actions ...chromedp.Action) error {
		for _, a := range actions {
			if err := a.Do(cdp.WithExecutor(ctx, exec)); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *CDPSuite) TestSetCookies() {
	exec := &cookieExecutor{}
	s.runWithExecutor(exec)

	err := s.client.SetCookies(context.Background(), []Cookie{
		{
			Domain: ".example.com", Name: "sid", Value: "abc", Path: "/",
			Expires: 1893456000, Secure: true, HTTPOnly: true, SameSite: "Lax",
		},
		{
			// No expiry and no SameSite: both fields stay unset rather than
			// being sent as a zero, which Chrome would read as 1970.
			Domain: "other.example", Name: "n", Value: "v", Path: "/x",
		},
	})
	require.NoError(s.T(), err)

	require.Equal(s.T(), string(storage.CommandSetCookies), exec.method)
	require.NotNil(s.T(), exec.params)
	require.Len(s.T(), exec.params.Cookies, 2)

	first := exec.params.Cookies[0]
	require.Equal(s.T(), ".example.com", first.Domain)
	require.Equal(s.T(), "sid", first.Name)
	require.Equal(s.T(), "abc", first.Value)
	require.Equal(s.T(), "/", first.Path)
	require.True(s.T(), first.Secure)
	require.True(s.T(), first.HTTPOnly)
	require.Equal(s.T(), "Lax", string(first.SameSite))
	require.NotNil(s.T(), first.Expires)
	require.Equal(s.T(), time.Unix(1893456000, 0), time.Time(*first.Expires))

	second := exec.params.Cookies[1]
	require.Nil(s.T(), second.Expires)
	require.Empty(s.T(), string(second.SameSite))
}

// Nothing to install is not a round trip.
func (s *CDPSuite) TestSetCookiesEmpty() {
	s.setRunFn(func(context.Context, ...chromedp.Action) error {
		s.Fail("runFn must not be called for an empty cookie list")
		return nil
	})

	require.NoError(s.T(), s.client.SetCookies(context.Background(), nil))
}

// The count belongs in the error; the names and values never do.
func (s *CDPSuite) TestSetCookiesError() {
	exec := &cookieExecutor{err: errors.New("browser closed")}
	s.runWithExecutor(exec)

	err := s.client.SetCookies(context.Background(), []Cookie{{Domain: "example.com", Name: "sid", Value: "secret"}})
	require.ErrorContains(s.T(), err, "setting 1 cookies")
	require.ErrorContains(s.T(), err, "browser closed")
	require.NotContains(s.T(), err.Error(), "secret")
}
