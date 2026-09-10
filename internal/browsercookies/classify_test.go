package browsercookies

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type ClassifySuite struct {
	suite.Suite
}

func TestClassifySuite(t *testing.T) {
	suite.Run(t, new(ClassifySuite))
}

func (s *ClassifySuite) TestClassify() {
	c := NewClassifier(nil)

	tests := []struct {
		name   string
		domain string
		want   Category
	}{
		// Rows shaped like a real picker's. These are the cases a regression
		// would quietly reclassify as safe, which is why they are named.
		{"identity tenant keeps its label", "example.okta.com", CategorySignin},
		{"auth0 tenant", "example.eu.auth0.com", CategorySignin},
		{"duo endpoint", "api-1a2b3c4d.duosecurity.com", CategorySignin},
		{"oauth host prefix", "oauth.officeapps.live.com", CategorySignin},
		{"unlisted host under a listed parent", "usc-excel.officeapps.live.com", CategorySignin},
		{"keyword catches an unlisted bank", "enablebanking.com", CategoryBank},
		{"seed catches a bank a regex never would", "n26.com", CategoryBank},
		{"buy-now-pay-later", "affirm.com", CategoryBank},
		{"multi-part suffix in the seed list", "fidelity.co.uk", CategoryBank},

		{"mailbox", "google.com", CategoryEmail},
		{"mailbox subdomain inherits", "mail.google.com", CategoryEmail},
		{"password vault", "lastpass.com", CategorySignin},
		{"login host prefix", "login.live.com", CategorySignin},
		{"leading dot is ignored", ".stripe.com", CategoryBank},
		{"case is ignored", "STRIPE.COM", CategoryBank},

		{"ordinary site", "news.ycombinator.com", CategoryNone},
		{"ordinary site with a deep host", "cdn.assets.example.org", CategoryNone},
		{"empty", "", CategoryNone},
		{"bare label", "localhost", CategoryNone},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.Equal(tt.want, c.Classify(tt.domain))
		})
	}
}

// A prefix rule must not fire on a two-label domain: "login.com" is a site,
// not an authentication host for some parent.
func (s *ClassifySuite) TestSigninPrefixNeedsAParent() {
	s.Equal(CategoryNone, signinPrefixCategory("sso.io"))
	s.Equal(CategorySignin, signinPrefixCategory("sso.example.com"))
	s.Equal(CategoryNone, signinPrefixCategory("example"))
	s.Equal(CategoryNone, signinPrefixCategory("cdn.example.com"))
}

func (s *ClassifySuite) TestUserSensitiveDomains() {
	c := NewClassifier([]string{" .my-credit-union.example ", "", "other.example"})

	s.Equal(CategorySensitive, c.Classify("my-credit-union.example"))
	s.Equal(CategorySensitive, c.Classify("secure.my-credit-union.example"))
	s.Equal(CategorySensitive, c.Classify("other.example"))
	s.Equal(CategoryNone, c.Classify("unrelated.example"))
}

// The user's list wins over everything, including a seed entry, so a site
// can be escalated but never quietly downgraded.
func (s *ClassifySuite) TestUserListTakesPrecedence() {
	c := NewClassifier([]string{"google.com"})
	s.Equal(CategorySensitive, c.Classify("google.com"))
}

func (s *ClassifySuite) TestParseSeedList() {
	got := parseSeedList("# comment\n\nbank example.com\nsignin id.example\nnokeyword\n  email  spaced.example  \n")

	s.Equal(map[string]Category{
		"example.com":    CategoryBank,
		"id.example":     CategorySignin,
		"spaced.example": CategoryEmail,
	}, got)
}

// The embedded list is data, and data rots silently. This asserts it parses
// and that every line uses a category the UI knows how to badge.
func (s *ClassifySuite) TestEmbeddedSeedListIsWellFormed() {
	seed := parseSeedList(seedList)
	require.NotEmpty(s.T(), seed)

	valid := map[Category]bool{CategoryEmail: true, CategorySignin: true, CategoryBank: true}
	for domain, cat := range seed {
		s.True(valid[cat], "domain %q has unknown category %q", domain, cat)
		s.NotContains(domain, " ", "domain %q has a stray space", domain)
	}
}
