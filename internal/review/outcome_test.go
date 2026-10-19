package review

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type OutcomeSuite struct {
	suite.Suite
}

func TestOutcomeSuite(t *testing.T) {
	suite.Run(t, new(OutcomeSuite))
}

func (s *OutcomeSuite) TestDeriveOutcome() {
	nit := &Comment{ID: "nit", Source: "agent", Verdict: VerdictReal, Impact: ImpactNiceToHave}
	block := &Comment{ID: "block", Source: "agent", Verdict: VerdictReal, Impact: ImpactBlocking}
	cases := []struct {
		name     string
		sess     *Session
		want     Outcome
		wantText string
	}{
		{
			name:     "nothing left on the reviewed head",
			sess:     &Session{HeadSHA: "abc", ReviewedSHA: "abc"},
			want:     Outcome{State: OutcomeApprove},
			wantText: "approve: nothing left open",
		},
		{
			name: "dismissed, resolved, replies and nil comments don't count",
			sess: &Session{HeadSHA: "abc", ReviewedSHA: "abc", Comments: []*Comment{
				nil,
				{ID: "fp", Source: "agent", Verdict: VerdictFalsePositive},
				{ID: "fixed", Source: "agent", Verdict: VerdictAlreadyFixed},
				{ID: "done", Source: "agent", Verdict: VerdictReal, Impact: ImpactBlocking, Pushed: true, Resolved: true},
				{ID: "reply", Source: "github", GitHubID: 2, ReplyTo: 1},
				{ID: "unsynced", Source: "github"},
			}},
			want:     Outcome{State: OutcomeApprove},
			wantText: "approve: nothing left open",
		},
		{
			name:     "only nits",
			sess:     &Session{HeadSHA: "abc", ReviewedSHA: "abc", Comments: []*Comment{nit}},
			want:     Outcome{State: OutcomeApproveWithNits, Nits: []string{"nit"}},
			wantText: "approve with nits: 1 nice to have (nit)",
		},
		{
			name:     "a blocking finding wins over an unreviewed head",
			sess:     &Session{HeadSHA: "abd", ReviewedSHA: "abc", Comments: []*Comment{nit, block}},
			want:     Outcome{State: OutcomeRequestChanges, Blocking: []string{"block"}, Nits: []string{"nit"}},
			wantText: "request changes: 1 blocking (block)",
		},
		{
			name:     "never reviewed",
			sess:     &Session{HeadSHA: "abc"},
			want:     Outcome{State: OutcomeUnknown, Reason: "the head commit isn't reviewed yet"},
			wantText: "unknown: the head commit isn't reviewed yet",
		},
		{
			name: "a real finding without an impact, and an unjudged one, are pending",
			sess: &Session{HeadSHA: "abc", ReviewedSHA: "abc", Comments: []*Comment{
				{ID: "real", Source: "agent", Verdict: VerdictReal},
				{ID: "new", Source: "agent"},
			}},
			want:     Outcome{State: OutcomeUnknown, Pending: []string{"real", "new"}, Reason: "2 comment(s) not judged yet"},
			wantText: "unknown: 2 comment(s) not judged yet",
		},
		{
			name: "github threads count by their assessment",
			sess: &Session{HeadSHA: "abc", ReviewedSHA: "abc", Comments: []*Comment{
				{ID: "gh-1", Source: "github", GitHubID: 1},
				{ID: "gh-2", Source: "github", GitHubID: 2},
				{ID: "gh-3", Source: "github", GitHubID: 3},
				{ID: "gh-4", Source: "github", GitHubID: 4},
			}, Threads: map[int64]ThreadAssessment{
				1: {Status: ThreadAddressed},
				2: {Status: ThreadOpen, Impact: ImpactBlocking},
				3: {Status: ThreadOpen, Impact: ImpactNiceToHave},
			}},
			want:     Outcome{State: OutcomeRequestChanges, Blocking: []string{"gh-2"}, Nits: []string{"gh-3"}, Pending: []string{"gh-4"}},
			wantText: "request changes: 1 blocking (gh-2)",
		},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			want := tc.want
			want.ReviewedSHA = tc.sess.ReviewedSHA
			for _, l := range []*[]string{&want.Blocking, &want.Nits, &want.Pending} {
				if *l == nil {
					*l = []string{}
				}
			}
			got := DeriveOutcome(tc.sess)
			require.Equal(s.T(), want, got)
			require.Equal(s.T(), tc.wantText, got.Summary())
		})
	}
}

func (s *OutcomeSuite) TestKnownThreadStatus() {
	require.True(s.T(), KnownThreadStatus(ThreadAddressed))
	require.True(s.T(), KnownThreadStatus(ThreadOpen))
	require.False(s.T(), KnownThreadStatus("wontfix"))
}

func (s *OutcomeSuite) TestEncodeDecodeThreads() {
	require.Empty(s.T(), encodeThreads(nil))
	require.Nil(s.T(), decodeThreads(""))
	require.Nil(s.T(), decodeThreads("{not json"))
	threads := map[int64]ThreadAssessment{9: {Status: ThreadOpen, Impact: ImpactNiceToHave, Reason: "naming"}, 2: {Status: ThreadAddressed}}
	enc := encodeThreads(threads)
	require.Equal(s.T(), `{"2":{"status":"addressed"},"9":{"status":"open","impact":"nice_to_have","reason":"naming"}}`, enc)
	require.Equal(s.T(), threads, decodeThreads(enc))
}

// Only the first comment of a GitHub thread a person started takes an
// assessment; an impact stays only on an open one.
func (s *OutcomeSuite) TestAssessThread() {
	cases := []struct {
		name    string
		channel string
		id      string
		a       ThreadAssessment
		want    map[int64]ThreadAssessment
	}{
		{name: "open thread", channel: "ch1", id: "gh-1", a: ThreadAssessment{Status: ThreadOpen, Impact: ImpactBlocking, Reason: "r"}, want: map[int64]ThreadAssessment{1: {Status: ThreadOpen, Impact: ImpactBlocking, Reason: "r"}}},
		{name: "addressed drops the impact", channel: "ch1", id: "gh-1", a: ThreadAssessment{Status: ThreadAddressed, Impact: ImpactBlocking}, want: map[int64]ThreadAssessment{1: {Status: ThreadAddressed}}},
		{name: "a reply", channel: "ch1", id: "gh-2", a: ThreadAssessment{Status: ThreadAddressed}},
		{name: "an agent comment", channel: "ch1", id: "a", a: ThreadAssessment{Status: ThreadAddressed}},
		{name: "a comment not on GitHub", channel: "ch1", id: "gh-0", a: ThreadAssessment{Status: ThreadAddressed}},
		{name: "unknown comment", channel: "ch1", id: "zz", a: ThreadAssessment{Status: ThreadAddressed}},
		{name: "no session", channel: "nope", id: "gh-1", a: ThreadAssessment{Status: ThreadAddressed}},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			store := NewStore()
			store.Put("ch1", &Session{Comments: []*Comment{
				{ID: "gh-1", Source: "github", GitHubID: 1},
				{ID: "gh-2", Source: "github", GitHubID: 2, ReplyTo: 1},
				{ID: "gh-0", Source: "github"},
				{ID: "a", Source: "agent", GitHubID: 3},
			}})
			before := store.Get("ch1")

			got := store.AssessThread(tc.channel, tc.id, tc.a)

			require.Equal(s.T(), tc.want != nil, got)
			require.Equal(s.T(), tc.want, store.Get("ch1").Threads)
			require.Nil(s.T(), before.Threads)
		})
	}
}

func (s *OutcomeSuite) TestStartVerifyMarkReviewedMarkApproved() {
	store := NewStore()
	require.False(s.T(), store.StartVerify("ch1"))
	require.False(s.T(), store.MarkReviewed("ch1"))
	require.False(s.T(), store.MarkApproved("ch1", "abc"))

	store.Put("ch1", &Session{HeadSHA: "abc", Threads: map[int64]ThreadAssessment{1: {Status: ThreadAddressed}}})
	require.True(s.T(), store.StartVerify("ch1"))
	require.True(s.T(), store.MarkReviewed("ch1"))
	require.True(s.T(), store.MarkApproved("ch1", "abc"))
	sess := store.Get("ch1")
	require.Nil(s.T(), sess.Threads)
	require.Equal(s.T(), "abc", sess.ReviewedSHA)
	require.Equal(s.T(), "abc", sess.ApprovedSHA)
}
