package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/spf13/cobra"
)

func (a *app) newUICmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ui",
		Short: "Drive the open desktop app windows",
	}
	cmd.AddCommand(a.newUIRunCmd(), a.newUIStateCmd())
	return cmd
}

func (a *app) newUIRunCmd() *cobra.Command {
	var clientID, timeout string
	cmd := &cobra.Command{
		Use:   "run [steps]",
		Short: "Run steps in an app window and print its results as JSON",
		Long: `Run steps in an app window and print its results as JSON. The steps are a
JSON array, from the argument or else stdin, and run in order until one
fails:

  {"op":"select_channel","channel_id":"…"}   open a channel, thread or worktree thread
  {"op":"set_tab","tab":"Chat"}               switch to a layout tab
  {"op":"add_pane","panel":"docker-agent","next_to":"chat","direction":"horizontal","open_mode":"fresh"}
  {"op":"replace_pane","pane":"chat","panel":"docker-agent","open_mode":"fresh"}
  {"op":"remove_pane","pane":"git"}
  {"op":"maximize_pane","pane":"docker-agent"}  make a pane fill the tab
  {"op":"restore_pane"}                       put it back
  {"op":"open_file","path":"cmd/main.go","line":12}
  {"op":"add_pane","panel":"playground","item":"board","scope":"project"}
  {"op":"send_input","pane":"docker-agent","text":"…","submit":true}
  {"op":"read_output","pane":"docker-agent","lines":50}
  {"op":"wait_for","pane":"docker-agent","match":"regexp","quiet_ms":2000}

A pane is a pane id or a panel type, the first pane of that type. The
terminal steps (send_input, read_output, wait_for) use only docker-agent and
docker-shell panes, never a host shell. wait_for waits for output matching
match, or else for the terminal to be quiet for quiet_ms; after a send_input
to the pane in the same command, only output since counts. The steps go to
the focused window unless --client names another; ui:state lists them.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			var steps []byte
			if len(args) == 1 && args[0] != "-" {
				steps = []byte(args[0])
			} else {
				var err error
				if steps, err = io.ReadAll(c.InOrStdin()); err != nil {
					return fmt.Errorf("reading the steps: %w", err)
				}
			}
			if !json.Valid(steps) {
				return errors.New("the steps aren't valid JSON")
			}
			return a.runUISteps(c.Context(), c.OutOrStdout(), clientID, timeout, steps)
		},
	}
	cmd.Flags().StringVar(&clientID, "client", "", "Client id of the window to drive (default: the focused one)")
	cmd.Flags().StringVar(&timeout, "timeout", "", "How long to wait for the window, as a Go duration (default 1m, at most 10m)")
	return cmd
}

// uiStepResult is the part of a step's result the CLI reads.
type uiStepResult struct {
	OK bool `json:"ok"`
}

func (a *app) runUISteps(ctx context.Context, out io.Writer, clientID, timeout string, steps []byte) error {
	body, _ := json.Marshal(map[string]any{ // a map of strings and raw JSON always marshals
		"client_id": clientID,
		"steps":     json.RawMessage(steps),
		"timeout":   timeout,
	})
	data, err := a.uiRequest(ctx, http.MethodPost, "/api/ui/commands", bytes.NewReader(body))
	if err != nil {
		return err
	}
	var reply struct {
		Results []uiStepResult `json:"results"`
		Error   string         `json:"error"`
	}
	if err := json.Unmarshal(data, &reply); err != nil {
		return fmt.Errorf("parsing the results: %w", err)
	}
	if err := writeIndentedJSON(out, data); err != nil {
		return err
	}
	if reply.Error != "" {
		return errors.New(reply.Error)
	}
	for i, r := range reply.Results {
		if !r.OK {
			return fmt.Errorf("step %d failed", i+1)
		}
	}
	return nil
}

func (a *app) newUIStateCmd() *cobra.Command {
	var watch bool
	cmd := &cobra.Command{
		Use:   "state",
		Short: "Print the open app windows and their state as JSON",
		Long: `Print the open app windows and their state as JSON: each window's open
channel, layout tabs and panes, the window a command goes to by default
first. With --watch, print the state again, one JSON line each time, whenever
it changes.`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			if !watch {
				data, err := a.uiRequest(c.Context(), http.MethodGet, "/api/ui/state", nil)
				if err != nil {
					return err
				}
				return writeIndentedJSON(c.OutOrStdout(), data)
			}
			return a.watchUIState(c.Context(), c.OutOrStdout())
		},
	}
	cmd.Flags().BoolVar(&watch, "watch", false, "Keep printing the state as it changes")
	return cmd
}

// watchUIState prints the state, then each newer one, until ctx is done.
func (a *app) watchUIState(ctx context.Context, out io.Writer) error {
	path := "/api/ui/state"
	printed := false
	var last uint64
	for {
		data, err := a.uiRequest(ctx, http.MethodGet, path, nil)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		var snap struct {
			Version uint64 `json:"version"`
		}
		if err := json.Unmarshal(data, &snap); err != nil {
			return fmt.Errorf("parsing the state: %w", err)
		}
		// A wait that ran out answers with the version it waited past.
		if !printed || snap.Version != last {
			var line bytes.Buffer
			_ = json.Compact(&line, data) // the daemon's JSON is valid
			if _, err := fmt.Fprintln(out, line.String()); err != nil {
				return err
			}
			printed = true
		}
		last = snap.Version
		path = "/api/ui/state?after=" + url.QueryEscape(fmt.Sprint(last))
	}
}

// uiRequest calls the daemon's UI route at path and returns the body of a
// 200 answer.
func (a *app) uiRequest(ctx context.Context, method, path string, body io.Reader) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, a.resolveAPIURL()+path, body)
	if err != nil {
		return nil, fmt.Errorf("building the request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.apiClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling the UI API: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading the answer: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(data)))
	}
	return data, nil
}

func writeIndentedJSON(out io.Writer, data []byte) error {
	var b bytes.Buffer
	if err := json.Indent(&b, data, "", "  "); err != nil {
		return fmt.Errorf("formatting the answer: %w", err)
	}
	b.WriteByte('\n')
	_, err := out.Write(b.Bytes())
	return err
}
