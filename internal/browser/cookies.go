package browser

import (
	"context"
	"fmt"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/storage"
	"github.com/chromedp/chromedp"
)

// Cookie is a cookie to install into a browser profile.
//
// It mirrors browsercookies.Cookie rather than reusing it: internal/browser
// is the CDP layer and internal/browsercookies is a leaf that knows nothing
// about loop, so the conversion lives in the caller and neither package has
// to import the other.
type Cookie struct {
	Domain   string
	Name     string
	Value    string
	Path     string
	Expires  int64 // Unix seconds
	Secure   bool
	HTTPOnly bool
	SameSite string // "Strict", "Lax", "None", or "" for unspecified
}

// SetCookies installs cookies into the browser this client is attached to.
//
// It uses the Storage domain rather than Network.setCookie because
// Storage.setCookies is browser-wide: one call covers every current and
// future tab in the profile, and it does not require first navigating to
// each cookie's origin. With a persistent profile mounted, Chrome then
// writes them to disk itself — loop never touches the profile's files.
func (c *CDPClient) SetCookies(ctx context.Context, cookies []Cookie) error {
	if len(cookies) == 0 {
		return nil
	}

	params := make([]*network.CookieParam, 0, len(cookies))
	for _, ck := range cookies {
		p := &network.CookieParam{
			Name:     ck.Name,
			Value:    ck.Value,
			Domain:   ck.Domain,
			Path:     ck.Path,
			Secure:   ck.Secure,
			HTTPOnly: ck.HTTPOnly,
		}
		if ck.Expires > 0 {
			expires := cdp.TimeSinceEpoch(time.Unix(ck.Expires, 0))
			p.Expires = &expires
		}
		if ck.SameSite != "" {
			p.SameSite = network.CookieSameSite(ck.SameSite)
		}
		params = append(params, p)
	}

	err := c.runFn(c.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		return storage.SetCookies(params).Do(ctx)
	}))
	if err != nil {
		return fmt.Errorf("setting %d cookies: %w", len(cookies), err)
	}
	return nil
}
