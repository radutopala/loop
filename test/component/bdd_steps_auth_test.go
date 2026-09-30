//go:build component

package component

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cucumber/godog"
	"github.com/gorilla/websocket"
)

func registerAuthSteps(ctx *godog.ScenarioContext, tc *TestContext) {
	ctx.Step(`^I send a GET request to "([^"]*)" without the API token$`, tc.sendGETWithoutToken)
	ctx.Step(`^I send a GET request to "([^"]*)" with the API token "([^"]*)"$`, tc.sendGETWithToken)
	ctx.Step(`^opening the WebSocket "([^"]*)" without the API token should fail with status (\d+)$`, tc.assertWSRejected)
	ctx.Step(`^opening the WebSocket "([^"]*)" with the API token as a subprotocol should succeed$`, tc.assertWSSubprotocol)
	ctx.Step(`^I fetch "([^"]*)" under the returned content link without the API token$`, tc.fetchUnderContentLink)
	ctx.Step(`^I create a symlink "([^"]*)" in the repo pointing to "([^"]*)"$`, tc.createRepoSymlink)
}

// sendRaw sends a request with the given Authorization header ("" for none),
// bypassing the harness client that signs in as the owner.
func (tc *TestContext) sendRaw(path, authorization string) error {
	req, err := http.NewRequest(http.MethodGet, tc.BaseURL+tc.resolvePlaceholders(path), nil)
	if err != nil {
		return err
	}
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("sending request: %w", err)
	}
	defer resp.Body.Close()
	tc.LastResponse = resp
	tc.LastStatus = resp.StatusCode
	tc.LastBody, err = io.ReadAll(resp.Body)
	tc.LastJSON = nil
	_ = json.Unmarshal(tc.LastBody, &tc.LastJSON)
	return err
}

func (tc *TestContext) sendGETWithoutToken(path string) error {
	return tc.sendRaw(path, "")
}

func (tc *TestContext) sendGETWithToken(path, token string) error {
	return tc.sendRaw(path, "Bearer "+token)
}

func (tc *TestContext) wsURL(path string) string {
	return strings.Replace(tc.BaseURL, "http://", "ws://", 1) + tc.resolvePlaceholders(path)
}

func (tc *TestContext) assertWSRejected(path string, status int) error {
	conn, resp, err := websocket.DefaultDialer.Dial(tc.wsURL(path), nil)
	if err == nil {
		conn.Close()
		return errors.New("the WebSocket opened without a token")
	}
	if resp == nil || resp.StatusCode != status {
		return fmt.Errorf("want handshake status %d, got %v (%v)", status, resp, err)
	}
	return nil
}

// assertWSSubprotocol opens a socket the way the browser does: the token
// rides in Sec-WebSocket-Protocol, and the server answers with "loop".
func (tc *TestContext) assertWSSubprotocol(path string) error {
	dialer := websocket.Dialer{Subprotocols: []string{"loop", "loop.token." + apiToken()}}
	conn, _, err := dialer.Dial(tc.wsURL(path), nil)
	if err != nil {
		return fmt.Errorf("dialing with the token subprotocol: %w", err)
	}
	defer conn.Close()
	if got := conn.Subprotocol(); got != "loop" {
		return fmt.Errorf("want subprotocol %q, got %q", "loop", got)
	}
	return nil
}

// fetchUnderContentLink GETs rel under the base_url of the last
// POST /api/content-caps response, with no token.
func (tc *TestContext) fetchUnderContentLink(rel string) error {
	base, _ := tc.LastJSON["base_url"].(string)
	if base == "" {
		return fmt.Errorf("the last response has no base_url: %s", tc.LastBody)
	}
	return tc.sendRaw(base+rel, "")
}

func (tc *TestContext) createRepoSymlink(name, target string) error {
	if tc.ChannelDir == "" {
		return errors.New("no channel dir set; use 'I set up a test channel via API for git repo' step first")
	}
	link := filepath.Join(tc.ChannelDir, name)
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		return err
	}
	return os.Symlink(target, link)
}
