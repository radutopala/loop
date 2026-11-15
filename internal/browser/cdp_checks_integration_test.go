//go:build integration

package browser

import (
	"context"
	"time"

	"github.com/stretchr/testify/require"
)

// checksPage holds checkboxes in each state, one of them styled the way to-do
// apps draw theirs: a transparent input over its own artwork.
const checksPage = `<html><head><title>Checks</title></head><body>
<input type="checkbox" aria-label="Plain">
<input type="checkbox" aria-label="Ticked" checked>
<input type="checkbox" aria-label="Styled" style="opacity:0;width:40px;height:40px">
<input type="checkbox" aria-label="Partly" id="partly">
<div role="switch" aria-checked="true" aria-label="Dark mode" tabindex="0" style="width:40px;height:20px">dark</div>
<script>document.getElementById("partly").indeterminate = true;</script>
</body></html>`

func (s *CDPIntegrationSuite) TestGetElementRefsCheckStates() {
	ctx := context.Background()
	require.NoError(s.T(), s.client.Navigate(ctx, s.testURL+"/checks"))

	var refs []ElementRef
	require.Eventually(s.T(), func() bool {
		var err error
		refs, err = s.client.GetElementRefs(ctx)
		return err == nil && len(refs) == 5
	}, 10*time.Second, 250*time.Millisecond, "refs: %+v", refs)

	got := map[string]string{}
	for _, r := range refs {
		got[r.Name] = r.Role + " " + r.Value
	}
	require.Equal(s.T(), map[string]string{
		"Plain":     "checkbox unchecked",
		"Ticked":    "checkbox checked",
		"Styled":    "checkbox unchecked",
		"Partly":    "checkbox mixed",
		"Dark mode": "switch checked",
	}, got)
}
