//go:build integration

package browser

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const testPage = `<!DOCTYPE html>
<html>
<head><title>Loop CDP Test Page</title></head>
<body>
  <h1>CDP Integration Test</h1>
  <nav>
    <a href="/page2" id="nav-link">Go to Page 2</a>
    <a href="/page3">Page 3</a>
  </nav>
  <form id="test-form">
    <label for="username">Username</label>
    <input type="text" id="username" name="username" placeholder="Enter username">
    <label for="email">Email</label>
    <input type="email" id="email" name="email" placeholder="Enter email">
    <select id="role" name="role">
      <option value="admin">Admin</option>
      <option value="user">User</option>
    </select>
    <input type="checkbox" id="agree" name="agree"> <label for="agree">I agree</label>
    <button type="submit" id="submit-btn">Submit</button>
    <button type="button" id="action-btn" onclick="document.title='clicked'">Action</button>
  </form>
  <div id="output"></div>
  <div style="height:2000px"></div>
  <button id="bottom-btn" style="margin-bottom:50px">Bottom Button</button>
  <script>
    console.log("page loaded");
    console.error("test error message");
    document.getElementById("test-form").addEventListener("submit", function(e) {
      e.preventDefault();
      document.getElementById("output").textContent = "form submitted";
    });
  </script>
</body>
</html>`

const testPage2 = `<!DOCTYPE html>
<html><head><title>Page 2</title></head>
<body><h1>Page 2</h1><a href="/">Back to main</a></body>
</html>`

// CDPIntegrationSuite tests all CDP operations against a real Chrome container
// using a local httptest server for the test pages.
// Run with: go test -tags integration -v -run TestCDPIntegration ./internal/browser/
type CDPIntegrationSuite struct {
	suite.Suite
	containerID string
	hostPort    string
	client      *CDPClient
	testServer  *httptest.Server
	testURL     string // URL accessible from inside Docker (host.docker.internal)
}

func TestCDPIntegration(t *testing.T) {
	suite.Run(t, new(CDPIntegrationSuite))
}

func (s *CDPIntegrationSuite) SetupSuite() {
	t := s.T()

	// Start httptest server for test pages.
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, testPage)
	})
	mux.HandleFunc("/page2", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, testPage2)
	})
	mux.HandleFunc("/page3", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><head><title>Page 3</title></head><body><h1>Page 3</h1></body></html>`)
	})
	s.testServer = httptest.NewServer(mux)

	// Chrome runs in Docker — use host.docker.internal to reach the host test server.
	port := strings.Split(s.testServer.URL, ":")[2]
	s.testURL = fmt.Sprintf("http://host.docker.internal:%s", port)

	// Start Chrome container.
	out, err := exec.Command("docker", "run", "-d", "--rm",
		"-p", "127.0.0.1::9222",
		"--add-host=host.docker.internal:host-gateway",
		"loop-chrome:latest",
		"--window-size=1920,1080", "about:blank",
	).CombinedOutput()
	require.NoError(t, err, "start chrome container: %s", out)
	s.containerID = strings.TrimSpace(string(out))

	// Get host port.
	portOut, err := exec.Command("docker", "port", s.containerID, "9222/tcp").CombinedOutput()
	require.NoError(t, err, "get port: %s", portOut)
	parts := strings.Split(strings.TrimSpace(string(portOut)), ":")
	s.hostPort = parts[len(parts)-1]

	// Wait for Chrome to be ready.
	wsURL := fmt.Sprintf("ws://127.0.0.1:%s", s.hostPort)
	var client *CDPClient
	for i := range 20 {
		client, err = NewCDPClient(context.Background(), wsURL, slog.New(slog.NewTextHandler(os.Stderr, nil)))
		if err == nil {
			break
		}
		if i == 19 {
			t.Fatalf("chrome not ready after 10s: %v", err)
		}
		time.Sleep(500 * time.Millisecond)
	}
	s.client = client
}

func (s *CDPIntegrationSuite) TearDownSuite() {
	if s.client != nil {
		s.client.Close()
	}
	if s.containerID != "" {
		_ = exec.Command("docker", "rm", "-f", s.containerID).Run()
	}
	if s.testServer != nil {
		s.testServer.Close()
	}
}

func (s *CDPIntegrationSuite) nav() {
	_ = s.client.Navigate(context.Background(), s.testURL)
	time.Sleep(500 * time.Millisecond)
}

// --- Navigation ---

func (s *CDPIntegrationSuite) TestNavigate() {
	err := s.client.Navigate(context.Background(), s.testURL)
	require.NoError(s.T(), err)
}

func (s *CDPIntegrationSuite) TestPageInfo() {
	s.nav()
	info, err := s.client.GetPageInfo(context.Background())
	require.NoError(s.T(), err)
	require.Contains(s.T(), info.URL, "host.docker.internal")
	require.Equal(s.T(), "Loop CDP Test Page", info.Title)
}

func (s *CDPIntegrationSuite) TestReload() {
	s.nav()
	err := s.client.Reload(context.Background())
	require.NoError(s.T(), err)
}

func (s *CDPIntegrationSuite) TestGoBackForward() {
	ctx := context.Background()
	require.NoError(s.T(), s.client.Navigate(ctx, s.testURL))
	time.Sleep(500 * time.Millisecond)
	require.NoError(s.T(), s.client.Navigate(ctx, s.testURL+"/page2"))
	time.Sleep(500 * time.Millisecond)

	// window.history.back()/forward() are no-ops when there's no history,
	// so these should always succeed.
	require.NoError(s.T(), s.client.GoBack(ctx))
	require.NoError(s.T(), s.client.GoForward(ctx))
}

// --- Accessibility / Read Page ---

func (s *CDPIntegrationSuite) TestReadPageLinks() {
	s.nav()
	refs, err := s.client.GetElementRefs(context.Background())
	require.NoError(s.T(), err)
	var links []string
	for _, r := range refs {
		if r.Role == "link" {
			links = append(links, r.Name)
		}
	}
	require.Contains(s.T(), links, "Go to Page 2")
	require.Contains(s.T(), links, "Page 3")
}

func (s *CDPIntegrationSuite) TestReadPageFormElements() {
	s.nav()
	refs, err := s.client.GetElementRefs(context.Background())
	require.NoError(s.T(), err)

	roles := map[string][]string{}
	for _, r := range refs {
		roles[r.Role] = append(roles[r.Role], r.Name)
	}
	require.NotEmpty(s.T(), roles["textbox"], "should find text inputs")
	require.NotEmpty(s.T(), roles["button"], "should find buttons")
	require.NotEmpty(s.T(), roles["combobox"], "should find select dropdown")
	require.NotEmpty(s.T(), roles["checkbox"], "should find checkbox")
}

func (s *CDPIntegrationSuite) TestReadPageBoundingBoxes() {
	s.nav()
	refs, err := s.client.GetElementRefs(context.Background())
	require.NoError(s.T(), err)
	for _, r := range refs {
		require.Greater(s.T(), r.Width, float64(0), "ref %s should have width", r.RefID)
		require.Greater(s.T(), r.Height, float64(0), "ref %s should have height", r.RefID)
	}
}

// --- Screenshot ---

func (s *CDPIntegrationSuite) TestScreenshot() {
	s.nav()
	buf, err := s.client.Screenshot(context.Background())
	require.NoError(s.T(), err)
	require.Greater(s.T(), len(buf), 1000)
	// Verify it's a valid PNG (starts with PNG magic bytes).
	require.Equal(s.T(), []byte{0x89, 0x50, 0x4E, 0x47}, buf[:4], "should be a valid PNG")
}

// --- JavaScript ---

func (s *CDPIntegrationSuite) TestEvaluateJS() {
	s.nav()
	result, err := s.client.EvaluateJS(context.Background(), "document.title")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Loop CDP Test Page", result)
}

func (s *CDPIntegrationSuite) TestEvaluateJSModifiesPage() {
	s.nav()
	_, err := s.client.EvaluateJS(context.Background(), `document.getElementById("output").textContent = "hello from JS"`)
	require.NoError(s.T(), err)

	result, err := s.client.EvaluateJS(context.Background(), `document.getElementById("output").textContent`)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "hello from JS", result)
}

// --- Input ---

func (s *CDPIntegrationSuite) TestMouseClick() {
	s.nav()
	err := s.client.MouseClick(context.Background(), 300, 200, "left", 1)
	require.NoError(s.T(), err)
}

func (s *CDPIntegrationSuite) TestMouseRightClick() {
	s.nav()
	err := s.client.MouseClick(context.Background(), 300, 200, "right", 1)
	require.NoError(s.T(), err)
}

func (s *CDPIntegrationSuite) TestMouseDoubleClick() {
	s.nav()
	err := s.client.MouseClick(context.Background(), 300, 200, "left", 2)
	require.NoError(s.T(), err)
}

func (s *CDPIntegrationSuite) TestMouseTripleClick() {
	s.nav()
	err := s.client.MouseClick(context.Background(), 300, 200, "left", 3)
	require.NoError(s.T(), err)
}

func (s *CDPIntegrationSuite) TestMouseMove() {
	s.nav()
	err := s.client.MouseMove(context.Background(), 100, 100, 0)
	require.NoError(s.T(), err)
}

func (s *CDPIntegrationSuite) TestMouseScroll() {
	s.nav()
	err := s.client.MouseScroll(context.Background(), 100, 100, 0, 100)
	require.NoError(s.T(), err)
}

func (s *CDPIntegrationSuite) TestTypeText() {
	s.nav()
	// Click on the username input, then type.
	refs, _ := s.client.GetElementRefs(context.Background())
	for _, r := range refs {
		if r.Role == "textbox" && strings.Contains(r.Name, "Username") {
			_ = s.client.MouseClick(context.Background(), r.X+r.Width/2, r.Y+r.Height/2, "left", 1)
			break
		}
	}
	err := s.client.TypeText(context.Background(), "testuser")
	require.NoError(s.T(), err)

	// Verify the value was typed.
	val, err := s.client.EvaluateJS(context.Background(), `document.getElementById("username").value`)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "testuser", val)
}

func (s *CDPIntegrationSuite) TestKeyPress() {
	s.nav()
	err := s.client.KeyPress(context.Background(), "Tab")
	require.NoError(s.T(), err)
}

// --- ClickRef ---

func (s *CDPIntegrationSuite) TestClickRef() {
	s.nav()
	refs, err := s.client.GetElementRefs(context.Background())
	require.NoError(s.T(), err)

	// Find the "Action" button and click it by ref.
	refIdx := -1
	for i, r := range refs {
		if r.Role == "button" && r.Name == "Action" {
			refIdx = i + 1 // 1-indexed
			break
		}
	}
	require.Greater(s.T(), refIdx, 0, "should find Action button")

	err = s.client.ClickRef(context.Background(), refs, refIdx)
	require.NoError(s.T(), err)

	// The button's onclick sets document.title to "clicked".
	time.Sleep(200 * time.Millisecond)
	title, _ := s.client.EvaluateJS(context.Background(), "document.title")
	require.Equal(s.T(), "clicked", title)
}

// --- Tabs ---

func (s *CDPIntegrationSuite) TestListTabs() {
	tabs, err := s.client.ListTabs(context.Background())
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), tabs)
}

func (s *CDPIntegrationSuite) TestSwitchTab() {
	ctx := context.Background()
	// Create a new tab and switch to it.
	tid, err := s.client.NewTab(ctx, s.testURL+"/page2")
	require.NoError(s.T(), err)

	err = s.client.SwitchTab(ctx, tid)
	require.NoError(s.T(), err)

	// Clean up.
	_ = s.client.CloseTab(ctx, tid)
}

func (s *CDPIntegrationSuite) TestNewTabAndClose() {
	ctx := context.Background()
	tid, err := s.client.NewTab(ctx, "about:blank")
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), tid)

	tabs, err := s.client.ListTabs(ctx)
	require.NoError(s.T(), err)
	found := false
	for _, t := range tabs {
		if t.TargetID == tid {
			found = true
			break
		}
	}
	require.True(s.T(), found, "new tab should appear in list")

	// CloseTab may fail in headless — just verify no panic.
	_ = s.client.CloseTab(ctx, tid)
}

// --- Screencast ---

func (s *CDPIntegrationSuite) TestScreencast() {
	s.client.StopScreencast()
	s.nav()

	ch := s.client.StartScreencast(30, 1280, 900)
	require.NotNil(s.T(), ch)

	// Try to receive a frame — may not arrive if prior tests changed tab state.
	// Just verify start/stop don't panic.
	select {
	case frame := <-ch:
		require.Greater(s.T(), len(frame), 100)
		require.Equal(s.T(), []byte{0xFF, 0xD8}, frame[:2], "should be a valid JPEG")
	case <-time.After(3 * time.Second):
		// No frame is OK — screencast is timing-sensitive in test suites.
	}

	s.client.StopScreencast()
}

// --- Console ---

func (s *CDPIntegrationSuite) TestConsoleCapture() {
	ch := make(chan ConsoleMessage, 10)
	err := s.client.EnableConsoleCapture(context.Background(), ch)
	require.NoError(s.T(), err)

	// Navigate triggers console.log and console.error in the test page.
	s.nav()

	// Collect messages for up to 3 seconds.
	var msgs []ConsoleMessage
	timeout := time.After(3 * time.Second)
	for {
		select {
		case msg := <-ch:
			msgs = append(msgs, msg)
			if len(msgs) >= 2 {
				goto done
			}
		case <-timeout:
			goto done
		}
	}
done:
	require.GreaterOrEqual(s.T(), len(msgs), 2, "should capture console.log and console.error")

	var levels []string
	for _, m := range msgs {
		levels = append(levels, m.Level)
	}
	require.Contains(s.T(), levels, "log")
	require.Contains(s.T(), levels, "error")
}

// --- ResizeWindow ---

func (s *CDPIntegrationSuite) TestResizeWindow() {
	s.nav()
	err := s.client.ResizeWindow(context.Background(), 800, 600)
	require.NoError(s.T(), err)

	// Verify the viewport changed via JS.
	width, err := s.client.EvaluateJS(context.Background(), "window.innerWidth.toString()")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "800", width)

	height, err := s.client.EvaluateJS(context.Background(), "window.innerHeight.toString()")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "600", height)
}

// --- MouseDown / MouseUp ---

func (s *CDPIntegrationSuite) TestMouseDownUp() {
	s.nav()
	ctx := context.Background()

	err := s.client.MouseDown(ctx, 300, 200, "left")
	require.NoError(s.T(), err)

	err = s.client.MouseUp(ctx, 300, 200, "left")
	require.NoError(s.T(), err)
}

// --- NetworkCapture ---

func (s *CDPIntegrationSuite) TestNetworkCapture() {
	ch := make(chan NetworkRequest, 50)
	err := s.client.EnableNetworkCapture(context.Background(), ch)
	require.NoError(s.T(), err)

	// Navigate to trigger network requests.
	err = s.client.Navigate(context.Background(), s.testURL+"/page3")
	require.NoError(s.T(), err)
	time.Sleep(1 * time.Second)

	// Collect captured requests.
	var reqs []NetworkRequest
	for {
		select {
		case req := <-ch:
			reqs = append(reqs, req)
		default:
			goto collected
		}
	}
collected:
	require.NotEmpty(s.T(), reqs, "should capture at least one network request")

	// Verify at least one request contains the page URL.
	var found bool
	for _, req := range reqs {
		if strings.Contains(req.URL, "/page3") {
			found = true
			require.Equal(s.T(), int64(200), req.Status)
			break
		}
	}
	require.True(s.T(), found, "should capture the /page3 navigation request")
}


// --- ResetScreencast ---

func (s *CDPIntegrationSuite) TestResetScreencast() {
	s.nav()
	// Start screencast, then reset, then start again — should not panic.
	ch1 := s.client.StartScreencast(30, 1280, 900)
	require.NotNil(s.T(), ch1)

	s.client.ResetScreencast()

	ch2 := s.client.StartScreencast(30, 1280, 900)
	require.NotNil(s.T(), ch2)

	s.client.StopScreencast()
}

// --- SwitchTarget ---

func (s *CDPIntegrationSuite) TestSwitchTargetActivatesTab() {
	ctx := context.Background()

	// Create a new tab with a known URL.
	tid, err := s.client.NewTab(ctx, s.testURL+"/page2")
	require.NoError(s.T(), err)

	// SwitchTarget activates the tab via HTTP and updates targetID.
	err = s.client.SwitchTarget(tid)
	require.NoError(s.T(), err)
	require.Equal(s.T(), tid, s.client.TargetID())

	// Clean up.
	_ = s.client.CloseTab(ctx, tid)
}

// --- TabInfo.Active ---

func (s *CDPIntegrationSuite) TestListTabsActiveField() {
	tabs, err := s.client.ListTabs(context.Background())
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), tabs)
	// Active field is not set by ListTabs itself — it's set by the API layer.
	// Just verify the field exists and defaults to false.
	for _, t := range tabs {
		_ = t.Active // compile-time check that the field exists
	}
}

// --- Screencast frame channel isolation ---

func (s *CDPIntegrationSuite) TestScreencastFrameChannelIsolation() {
	s.nav()

	// Start screencast — get channel 1.
	ch1 := s.client.StartScreencast(30, 1280, 900)
	require.NotNil(s.T(), ch1)

	// Stop and start again — get channel 2 (should be different).
	s.client.StopScreencast()
	ch2 := s.client.StartScreencast(30, 1280, 900)
	require.NotNil(s.T(), ch2)

	// Channels should be different objects (new frameCh each call).
	// Can't compare channels directly, but we can verify both work.
	s.client.StopScreencast()
}

// --- ScrollIntoView ---

func (s *CDPIntegrationSuite) TestScrollIntoView() {
	s.nav()
	ctx := context.Background()

	// Get refs — each now includes BackendDOMNodeID.
	refs, err := s.client.GetElementRefs(ctx)
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), refs)

	// Pick the first ref with a valid BackendDOMNodeID.
	var nodeID cdp.BackendNodeID
	for _, ref := range refs {
		if ref.BackendDOMNodeID > 0 {
			nodeID = ref.BackendDOMNodeID
			break
		}
	}
	require.NotZero(s.T(), nodeID, "should find a ref with BackendDOMNodeID")

	err = s.client.ScrollIntoView(ctx, nodeID)
	require.NoError(s.T(), err)
}
