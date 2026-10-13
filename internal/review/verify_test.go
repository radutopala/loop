package review

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type VerifySuite struct {
	suite.Suite
}

func TestVerifySuite(t *testing.T) {
	suite.Run(t, new(VerifySuite))
}

func (s *VerifySuite) TestHasAgentComments() {
	cases := []struct {
		name     string
		comments []*Comment
		want     bool
	}{
		{name: "none", comments: nil, want: false},
		{name: "only github", comments: []*Comment{{ID: "gh-1", Source: "github"}, nil}, want: false},
		{name: "an agent one", comments: []*Comment{{ID: "gh-1", Source: "github"}, {ID: "a", Source: "agent"}}, want: true},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.want, HasAgentComments(tc.comments))
		})
	}
}

func (s *VerifySuite) TestBuildVerifyPrompt() {
	cases := []struct {
		name         string
		instructions string
		vc           VerifyContext
		want         string
	}{
		{
			name: "default instructions and the full context",
			vc:   VerifyContext{PRNumber: 7, PRURL: "https://github.com/o/r/pull/7", HeadSHA: "abc123", Checkout: "/work/pr-7", Dedup: `{"removed":[]}`},
			want: DefaultVerifyPrompt + "\n\nReview context:\n- PR: #7\n- PR URL: https://github.com/o/r/pull/7\n- Reviewed commit: abc123\n- Checkout: /work/pr-7\n- Dedup pass: {\"removed\":[]}\n\nYour reply is posted to the channel as is.",
		},
		{
			name:         "custom instructions, context left empty",
			instructions: "  Judge each comment.\n",
			vc:           VerifyContext{Dedup: "failed: boom"},
			want:         "Judge each comment.\n\nReview context:\n- Dedup pass: failed: boom\n\nYour reply is posted to the channel as is.",
		},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.want, BuildVerifyPrompt(tc.instructions, tc.vc))
		})
	}
}
