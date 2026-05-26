package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"
)

// reviewHTTPClient is the subset of http.Client used by the review CLI.
// Defined so tests can inject a stub without standing up an httptest.Server,
// though the default flow uses httptest in cmd/loop tests for simplicity.
type reviewHTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// reviewCLIOutput is the JSON shape that `loop review run --wait` prints to
// stdout once the daemon flips to a terminal status. Workflow bash nodes
// parse this into runCtx.Review via internal/workflow.parseReviewOutput.
type reviewCLIOutput struct {
	Status     string                  `json:"status"`
	NoComments bool                    `json:"no_comments"`
	Comments   []reviewCLIOutputCommit `json:"comments"`
	Error      string                  `json:"error,omitempty"`
}

// reviewCLIOutputCommit is the minimal comment shape the workflow parser
// understands. We re-shape into this from the daemon's richer session
// payload so the schema is stable regardless of internal changes to
// review.Comment.
type reviewCLIOutputCommit struct {
	ID       string `json:"id"`
	Severity string `json:"severity,omitempty"`
	Path     string `json:"path,omitempty"`
	Line     int    `json:"line,omitempty"`
	Body     string `json:"body,omitempty"`
}

func (a *app) newReviewCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "review",
		Short: "Drive review-panel runs from the CLI",
	}
	cmd.AddCommand(a.newReviewRunCmd())
	return cmd
}

func (a *app) newReviewRunCmd() *cobra.Command {
	var channelID, apiURL, timeoutStr string
	var wait bool

	cmd := &cobra.Command{
		Use:   "run",
		Short: "Trigger a review run; with --wait, block until the daemon flips to a terminal status and print JSON.",
		RunE: func(c *cobra.Command, _ []string) error {
			timeout, err := time.ParseDuration(timeoutStr)
			if err != nil {
				return fmt.Errorf("invalid --timeout: %w", err)
			}
			resolvedURL := resolveReviewAPIURL(apiURL, os.Getenv("API_URL"))
			return a.runReview(c.Context(), c.OutOrStdout(), resolvedURL, channelID, wait, timeout)
		},
	}

	cmd.Flags().StringVar(&channelID, "channel-id", "", "Channel ID to run review on")
	cmd.Flags().StringVar(&apiURL, "api-url", "", "Loop API base URL (default $API_URL or http://localhost:8222)")
	cmd.Flags().BoolVar(&wait, "wait", false, "Block until the daemon flips to a terminal status, then print JSON")
	// 30m default chosen empirically: a real PR review on a non-trivial diff
	// regularly takes 5–15 minutes inside the agent container. The earlier 5m
	// default raced ahead of the daemon and killed the workflow's bash node
	// before fix/verify could fire (the daemon's review goroutine kept
	// running and the FE saw comments, but the loop body had already errored).
	cmd.Flags().StringVar(&timeoutStr, "timeout", "30m", "Maximum time to wait when --wait is set (Go duration)")
	_ = cmd.MarkFlagRequired("channel-id")

	return cmd
}

// resolveReviewAPIURL applies precedence: --api-url flag > $API_URL env >
// http://localhost:8222 default.
func resolveReviewAPIURL(flag, env string) string {
	if flag != "" {
		return flag
	}
	if env != "" {
		return env
	}
	return "http://localhost:8222"
}

// runReview POSTs to /api/channels/{id}/review/run and, when wait is true,
// polls /api/channels/{id}/review every second until the session leaves the
// "reviewing" status or the deadline fires.
//
// When wait is true the timeout bounds the whole operation: ctx is wrapped in
// context.WithTimeout so cancellation propagates into every client.Do call,
// not just the gap between polls. Without that wrap, a single hung response
// (network stall, daemon deadlock, partial write) would block inside
// client.Do indefinitely because http.DefaultClient has no Timeout set and
// cobra's parent ctx has no deadline either — the `time.Now().After(deadline)`
// check between polls never gets a turn to fire.
func (a *app) runReview(ctx context.Context, stdout io.Writer, apiURL, channelID string, wait bool, timeout time.Duration) error {
	if wait {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	client := a.reviewHTTPClient()

	runURL := fmt.Sprintf("%s/api/channels/%s/review/run", apiURL, channelID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, runURL, bytes.NewReader([]byte(`{}`)))
	if err != nil {
		return fmt.Errorf("building POST request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", runURL, err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("POST %s: unexpected status %d: %s", runURL, resp.StatusCode, string(body))
	}

	if !wait {
		return nil
	}

	// wrapTimeout swaps the deadline-exceeded sentinel for the documented
	// "timed out after --timeout" message so the user sees their flag in the
	// error, not raw `context deadline exceeded`. Other ctx errors (parent
	// cancel) pass through untouched. Used at every site where a deadline
	// can surface: in-flight Do error, top-of-loop guard, or sleep-select.
	wrapTimeout := func(err error) error {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("timed out after %s waiting for review to finish", timeout)
		}
		return err
	}

	getURL := fmt.Sprintf("%s/api/channels/%s/review", apiURL, channelID)
	for {
		if err := ctx.Err(); err != nil {
			return wrapTimeout(err)
		}
		out, terminal, perr := pollReviewOnce(ctx, client, getURL)
		if perr != nil {
			// Transport-level error (TCP reset, momentary daemon restart, proxy
			// 502, hung response cancelled by our own ctx timeout). With
			// --timeout 30m and a 1s poll cadence we make thousands of GETs;
			// treating any single hiccup as fatal would kill a review-fix loop
			// iteration on the first packet drop. Bail only when the run ctx is
			// genuinely done (cancellation or our own deadline); otherwise back
			// off briefly and keep polling — the overall context.WithTimeout
			// still bounds the wait.
			if errors.Is(perr, context.Canceled) || errors.Is(perr, context.DeadlineExceeded) ||
				ctx.Err() != nil {
				return wrapTimeout(perr)
			}
			t := time.NewTimer(a.reviewPollTransportBackoff)
			select {
			case <-ctx.Done():
				t.Stop()
				return wrapTimeout(ctx.Err())
			case <-t.C:
			}
			continue
		}
		if terminal {
			enc, _ := json.Marshal(out) // shape is fixed; cannot fail
			if _, err := fmt.Fprintln(stdout, string(enc)); err != nil {
				return fmt.Errorf("writing output: %w", err)
			}
			if out.Status == "error" {
				return errors.New(out.Error)
			}
			return nil
		}
		t := time.NewTimer(a.reviewPollInterval)
		select {
		case <-ctx.Done():
			t.Stop()
			return wrapTimeout(ctx.Err())
		case <-t.C:
		}
	}
}

// pollReviewOnce calls GET /api/channels/{id}/review once. It returns the
// CLI-shaped output, a flag indicating whether the session is in a terminal
// state (ready or error), and any transport-level error. A 200 with
// present=false is treated as a transient "not yet" — the caller keeps
// polling rather than failing.
func pollReviewOnce(ctx context.Context, client reviewHTTPClient, url string) (reviewCLIOutput, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return reviewCLIOutput{}, false, fmt.Errorf("building GET request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return reviewCLIOutput{}, false, fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return reviewCLIOutput{}, false, fmt.Errorf("GET %s: unexpected status %d: %s", url, resp.StatusCode, string(body))
	}

	var raw struct {
		Present bool `json:"present"`
		Session *struct {
			Status   string `json:"status"`
			Error    string `json:"error"`
			Comments []struct {
				ID   string `json:"id"`
				Path string `json:"path"`
				Line int    `json:"line"`
				Body string `json:"body"`
			} `json:"comments"`
		} `json:"session"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return reviewCLIOutput{}, false, fmt.Errorf("decoding %s: %w", url, err)
	}
	if !raw.Present || raw.Session == nil {
		return reviewCLIOutput{}, false, nil
	}

	switch raw.Session.Status {
	case "ready":
		out := reviewCLIOutput{Status: "ready"}
		for _, c := range raw.Session.Comments {
			out.Comments = append(out.Comments, reviewCLIOutputCommit{
				ID:   c.ID,
				Path: c.Path,
				Line: c.Line,
				Body: c.Body,
			})
		}
		out.NoComments = len(out.Comments) == 0
		return out, true, nil
	case "error":
		return reviewCLIOutput{Status: "error", Error: raw.Session.Error}, true, nil
	default:
		return reviewCLIOutput{}, false, nil
	}
}

// reviewHTTPClient returns the http client used by the review CLI. Pulled
// onto app via a method so tests can replace it via the app.reviewClient
// field without exporting the field name in production callsites.
func (a *app) reviewHTTPClient() reviewHTTPClient {
	if a.reviewClient != nil {
		return a.reviewClient
	}
	return http.DefaultClient
}
