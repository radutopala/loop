//go:build integration

package browser

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// crossSiteFrameURL is served from inside Chrome's container. localhost is
// another site than the host the test pages come from, so Chrome runs the
// frame in a process of its own, as it does a payment or captcha frame.
const crossSiteFrameURL = "http://localhost:8000/"

// framesPage holds a frame of each kind beside the page's own button: a
// same-origin srcdoc frame the in-page scan walks into, a same-site and a
// cross-site frame it cannot. Clicks in the frames are posted back to the page.
const framesPage = `<html><head><title>Frames</title></head>
<body style="margin:0">
<button>Outer</button>
<iframe srcdoc="<button>Same origin</button>" style="position:absolute;left:0;top:50px;width:300px;height:60px;border:0"></iframe>
<iframe src="%s/" style="position:absolute;left:100px;top:200px;width:400px;height:150px;border:0"></iframe>
<iframe src="%s" style="position:absolute;left:600px;top:200px;width:400px;height:150px;border:0"></iframe>
<script>window.clicks = []; addEventListener("message", (e) => clicks.push(e.data));</script>
</body></html>`

// framePage is a frame with a button and a field that report their clicks
// and input to the page.
func framePage(button string) string {
	return fmt.Sprintf(`<html><body style="margin:0"><button style="margin:10px" onclick="parent.postMessage('%[1]s', '*')">%[1]s</button><input aria-label="Card number" oninput="parent.postMessage('%[1]s: ' + this.value, '*')"></body></html>`, button)
}

// serveCrossSiteFrame answers crossSiteFrameURL from inside Chrome's container
// with socat, which the image ships.
func serveCrossSiteFrame(t *testing.T, containerID string) {
	t.Helper()
	body := framePage("Cross-site pay")
	resp := fmt.Sprintf("HTTP/1.0 200 OK\r\nContent-Type: text/html\r\nContent-Length: %d\r\n\r\n%s", len(body), body)
	write := exec.Command("docker", "exec", "-i", containerID, "sh", "-c", "cat > /tmp/frame.http")
	write.Stdin = strings.NewReader(resp)
	out, err := write.CombinedOutput()
	require.NoError(t, err, "write cross-site frame: %s", out)
	out, err = exec.Command("docker", "exec", "-d", containerID,
		"socat", "TCP-LISTEN:8000,bind=127.0.0.1,fork,reuseaddr", "SYSTEM:cat /tmp/frame.http").CombinedOutput()
	require.NoError(t, err, "serve cross-site frame: %s", out)
}

func (s *CDPIntegrationSuite) TestGetElementRefsCrossOriginFrames() {
	ctx := context.Background()
	require.NoError(s.T(), s.client.Navigate(ctx, s.testURL+"/frames"))

	var refs []ElementRef
	require.Eventually(s.T(), func() bool {
		var err error
		refs, err = s.client.GetElementRefs(ctx)
		return err == nil && len(refs) == 6
	}, 10*time.Second, 250*time.Millisecond, "refs: %+v", refs)

	type placed struct {
		Role, Name string
		X, Y       float64
	}
	var got []placed
	for _, r := range refs {
		got = append(got, placed{r.Role, r.Name, r.X, r.Y})
	}
	// The frames' rects are the page's: each frame's button sits 10px inside
	// its iframe, the field to its right.
	require.Equal(s.T(), "button", got[0].Role)
	require.Equal(s.T(), "Outer", got[0].Name)
	require.Equal(s.T(), placed{"button", "Same origin", got[1].X, got[1].Y}, got[1])
	require.Equal(s.T(), placed{"button", "Same-site pay", 110, 210}, got[2])
	require.Equal(s.T(), "textbox", got[3].Role)
	require.Equal(s.T(), "Card number", got[3].Name)
	require.Equal(s.T(), placed{"button", "Cross-site pay", 610, 210}, got[4])
	require.Equal(s.T(), "Card number", got[5].Name)
	require.Equal(s.T(), "ref_6", refs[5].RefID)

	// The out-of-process frame is still in the page after its session went.
	require.NoError(s.T(), s.client.ClickRef(ctx, refs, 5))
	require.NoError(s.T(), s.client.ClickRef(ctx, refs, 3))
	require.Eventually(s.T(), func() bool {
		clicks, err := s.client.EvaluateJS(ctx, "clicks.join(',')")
		return err == nil && clicks == "Cross-site pay,Same-site pay"
	}, 5*time.Second, 100*time.Millisecond)

	// Keys follow the click into the frame, as form_input sends them.
	require.NoError(s.T(), s.client.ClickRef(ctx, refs, 6))
	require.NoError(s.T(), s.client.TypeText(ctx, "42"))
	require.Eventually(s.T(), func() bool {
		clicks, err := s.client.EvaluateJS(ctx, "clicks.at(-1)")
		return err == nil && clicks == "Cross-site pay: 42"
	}, 5*time.Second, 100*time.Millisecond)

	again, err := s.client.GetElementRefs(ctx)
	require.NoError(s.T(), err)
	require.Len(s.T(), again, 6)
}
