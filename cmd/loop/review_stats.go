package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/radutopala/loop/internal/db"
)

// reviewStatsTimeout bounds the stats request: the daemon answers from the
// history in one query pass.
const reviewStatsTimeout = 30 * time.Second

func (a *app) newReviewStatsCmd() *cobra.Command {
	var channelID, apiURL, pr string
	var days int
	var asJSON bool

	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Print the review stats of the channel's repo: runs, findings, verdicts, impact and what became of the pushed ones, per model, effort, scope and category.",
		RunE: func(c *cobra.Command, _ []string) error {
			channelID = resolveReviewChannelID(channelID, os.Getenv("LOOP_CHANNEL_ID"))
			if channelID == "" {
				return fmt.Errorf("channel-id is required (pass --channel-id or set $LOOP_CHANNEL_ID)")
			}
			if days <= 0 {
				return fmt.Errorf("invalid --days %d: expected a positive number", days)
			}
			q := url.Values{"days": {strconv.Itoa(days)}}
			if pr != "" {
				n, err := parsePRNumber(pr)
				if err != nil {
					return err
				}
				q.Set("pr", strconv.Itoa(n))
			}
			return a.reviewStats(c.Context(), c.OutOrStdout(), resolveReviewAPIURL(apiURL, os.Getenv("LOOP_API_URL")), channelID, q, asJSON)
		},
	}

	cmd.Flags().StringVar(&channelID, "channel-id", "", "Channel ID whose repo's stats to print (default $LOOP_CHANNEL_ID)")
	cmd.Flags().StringVar(&apiURL, "api-url", "", "Loop API base URL (default $LOOP_API_URL or http://localhost:8222)")
	cmd.Flags().StringVar(&pr, "pr", "", "PR number or URL to limit the stats to (default: every PR of the repo)")
	cmd.Flags().IntVar(&days, "days", 90, "How many days back the stats cover")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Print the daemon's JSON instead of a table")

	return cmd
}

// reviewStats fetches the stats and prints them.
func (a *app) reviewStats(ctx context.Context, stdout io.Writer, apiURL, channelID string, q url.Values, asJSON bool) error {
	ctx, cancel := context.WithTimeout(ctx, reviewStatsTimeout)
	defer cancel()
	u := fmt.Sprintf("%s/api/channels/%s/review/stats?%s", apiURL, channelID, q.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return fmt.Errorf("building GET request: %w", err)
	}
	resp, err := a.reviewHTTPClient().Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", u, err)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: unexpected status %d: %s", u, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if asJSON {
		_, err = fmt.Fprintln(stdout, strings.TrimSpace(string(body)))
		return err
	}
	var stats db.ReviewStats
	if err := json.Unmarshal(body, &stats); err != nil {
		return fmt.Errorf("decoding %s: %w", u, err)
	}
	return writeReviewStats(stdout, &stats)
}

// writeReviewStats prints the stats as a summary and one table per
// breakdown.
func writeReviewStats(w io.Writer, st *db.ReviewStats) error {
	r, f := st.Runs, st.Findings
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "Runs: %d (%d completed, %d failed, %d cancelled, %d running) over %d PRs\n", r.Total, r.Completed, r.Failed, r.Cancelled, r.Running, r.PRs)
	fmt.Fprintf(tw, "Scope: %d full, %d incremental\n", r.Full, r.Incremental)
	fmt.Fprintf(tw, "Duration: median %s, p90 %s\n", reviewStatsDuration(r.MedianMs), reviewStatsDuration(r.P90Ms))
	fmt.Fprintf(tw, "Findings: %d added, %d folded, %d deleted, %d pushed\n", f.Added, f.Folded, f.Deleted, f.Pushed)
	fmt.Fprintf(tw, "Verdicts: %d real, %d false positive, %d already fixed, %d unjudged (false-positive rate %s)\n", f.Real, f.FalsePositive, f.AlreadyFixed, f.Unjudged, reviewStatsRate(f.FalsePositiveRate))
	fmt.Fprintf(tw, "Impact: %d blocking, %d nice to have\n", f.Blocking, f.NiceToHave)
	fmt.Fprintf(tw, "Pushed: %d resolved, %d outdated\n", f.Resolved, f.Outdated)
	for _, g := range []struct {
		name string
		rows []db.ReviewStatsGroup
		runs bool
	}{
		{"MODEL", st.ByModel, true},
		{"EFFORT", st.ByEffort, true},
		{"SCOPE", st.ByScope, true},
		{"CATEGORY", st.ByCategory, false},
	} {
		if len(g.rows) == 0 {
			continue
		}
		fmt.Fprintf(tw, "\n%s\tRUNS\tADDED\tREAL\tFALSE POS\tFP RATE\tPUSHED\tRESOLVED\n", g.name)
		for _, row := range g.rows {
			key, runs := row.Key, "-"
			if key == "" {
				key = "(unknown)"
			}
			if g.runs {
				runs = strconv.Itoa(row.Runs)
			}
			fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%s\t%d\t%d\n", key, runs, row.Added, row.Real, row.FalsePositive, reviewStatsRate(row.FalsePositiveRate), row.Pushed, row.Resolved)
		}
	}
	return tw.Flush()
}

func reviewStatsDuration(ms int64) string {
	if ms == 0 {
		return "-"
	}
	return (time.Duration(ms) * time.Millisecond).Round(time.Second).String()
}

func reviewStatsRate(r float64) string {
	return strconv.FormatFloat(r*100, 'f', 0, 64) + "%"
}
