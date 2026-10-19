package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"

	"github.com/stretchr/testify/require"
)

const reviewStatsJSON = `{
	"runs":{"total":4,"completed":3,"failed":1,"cancelled":0,"running":0,"full":3,"incremental":1,"prs":2,"median_ms":192400,"p90_ms":0},
	"findings":{"added":10,"folded":2,"deleted":1,"pushed":4,"real":5,"false_positive":1,"already_fixed":1,"unjudged":3,"blocking":2,"nice_to_have":3,"resolved":2,"outdated":1,"false_positive_rate":0.1428},
	"by_model":[{"key":"opus","runs":3,"added":8,"real":4,"false_positive":1,"false_positive_rate":0.2,"pushed":3,"resolved":2},{"key":"","runs":1,"added":2}],
	"by_effort":[],
	"by_scope":[{"key":"incremental","runs":1,"added":2}],
	"by_category":[{"key":"bug","added":6,"real":3,"pushed":2,"resolved":1}]
}`

func (s *MainSuite) TestNewReviewStatsCmd() {
	var gotQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/channels/ch1/review/stats":
			gotQuery = r.URL.RawQuery
			_, _ = io.WriteString(w, reviewStatsJSON)
		case "/api/channels/bad/review/stats":
			_, _ = io.WriteString(w, `{`)
		default:
			http.Error(w, "review history not configured", http.StatusNotImplemented)
		}
	}))
	defer ts.Close()
	s.app.reviewClient = http.DefaultClient

	table := "Runs: 4 (3 completed, 1 failed, 0 cancelled, 0 running) over 2 PRs\n" +
		"Scope: 3 full, 1 incremental\n" +
		"Duration: median 3m12s, p90 -\n" +
		"Findings: 10 added, 2 folded, 1 deleted, 4 pushed\n" +
		"Verdicts: 5 real, 1 false positive, 1 already fixed, 3 unjudged (false-positive rate 14%)\n" +
		"Impact: 2 blocking, 3 nice to have\n" +
		"Pushed: 2 resolved, 1 outdated\n" +
		"\n" +
		"MODEL      RUNS  ADDED  REAL  FALSE POS  FP RATE  PUSHED  RESOLVED\n" +
		"opus       3     8      4     1          20%      3       2\n" +
		"(unknown)  1     2      0     0          0%       0       0\n" +
		"\n" +
		"SCOPE        RUNS  ADDED  REAL  FALSE POS  FP RATE  PUSHED  RESOLVED\n" +
		"incremental  1     2      0     0          0%       0       0\n" +
		"\n" +
		"CATEGORY  RUNS  ADDED  REAL  FALSE POS  FP RATE  PUSHED  RESOLVED\n" +
		"bug       -     6      3     0          0%       2       1\n"

	cases := []struct {
		name      string
		args      []string
		wantErr   string
		wantOut   string
		wantQuery string
	}{
		{name: "channel id required", args: []string{"stats"}, wantErr: "channel-id is required"},
		{name: "bad days", args: []string{"stats", "--channel-id", "ch1", "--days", "0"}, wantErr: "invalid --days 0"},
		{name: "bad pr", args: []string{"stats", "--channel-id", "ch1", "--pr", "x"}, wantErr: `invalid --pr "x"`},
		{name: "bad url", args: []string{"stats", "--channel-id", "ch1", "--api-url", "http://a\x7f"}, wantErr: "building GET request"},
		{name: "unreachable", args: []string{"stats", "--channel-id", "ch1", "--api-url", "http://127.0.0.1:0"}, wantErr: "GET http://127.0.0.1:0"},
		{name: "not configured", args: []string{"stats", "--channel-id", "other", "--api-url", ts.URL}, wantErr: "unexpected status 501: review history not configured"},
		{name: "bad json", args: []string{"stats", "--channel-id", "bad", "--api-url", ts.URL}, wantErr: "decoding"},
		{name: "json", args: []string{"stats", "--channel-id", "ch1", "--api-url", ts.URL, "--json", "--pr", "https://github.com/o/r/pull/7", "--days", "30"},
			wantOut: `"total":4`, wantQuery: "days=30&pr=7"},
		{name: "table", args: []string{"stats", "--channel-id", "ch1", "--api-url", ts.URL}, wantOut: table, wantQuery: "days=90"},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			s.T().Setenv("LOOP_CHANNEL_ID", "")
			gotQuery = ""
			cmd := s.app.newReviewCmd()
			cmd.SetArgs(tc.args)
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(new(bytes.Buffer))
			err := cmd.Execute()
			if tc.wantErr != "" {
				require.ErrorContains(s.T(), err, tc.wantErr)
				return
			}
			require.NoError(s.T(), err)
			require.Contains(s.T(), out.String(), tc.wantOut)
			require.Equal(s.T(), tc.wantQuery, gotQuery)
		})
	}
}
