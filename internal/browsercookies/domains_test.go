package browsercookies

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

type DomainsSuite struct {
	suite.Suite
}

func TestDomainsSuite(t *testing.T) {
	suite.Run(t, new(DomainsSuite))
}

func (s *DomainsSuite) TestNormaliseDomain() {
	s.Equal("example.com", NormaliseDomain(".Example.com"))
	s.Equal("example.com", NormaliseDomain("example.com"))
	s.Empty(NormaliseDomain(""))
}

// Risky rows first, then the busiest, then alphabetically — the ordering is
// what stops a bank scrolling past unread in a 599-row list.
func (s *DomainsSuite) TestSummarise() {
	cookies := []Cookie{
		{Domain: ".example.com", Name: "a"},
		{Domain: "example.com", Name: "b"},
		{Domain: "example.com", Name: "c"},
		{Domain: "other.example", Name: "d"},
		{Domain: "beta.example", Name: "e"},
		{Domain: ".stripe.com", Name: "f"},
		{Domain: "oauth.officeapps.live.com", Name: "g"},
	}

	got := Summarise(cookies, NewClassifier(nil))

	s.Equal([]DomainSummary{
		{Domain: "oauth.officeapps.live.com", Count: 1, Category: CategorySignin},
		{Domain: "stripe.com", Count: 1, Category: CategoryBank},
		{Domain: "example.com", Count: 3},
		{Domain: "beta.example", Count: 1},
		{Domain: "other.example", Count: 1},
	}, got)
}

func (s *DomainsSuite) TestSummariseEmpty() {
	s.Empty(Summarise(nil, NewClassifier(nil)))
}

// Ticking a row grants exactly that scope. A subdomain listed as its own row
// and left unchecked must not ride along with its parent.
func (s *DomainsSuite) TestFilter() {
	cookies := []Cookie{
		{Domain: ".example.com", Name: "a"},
		{Domain: "login.example.com", Name: "b"},
		{Domain: "other.example", Name: "c"},
	}

	got := Filter(cookies, []string{".Example.com"})
	s.Require().Len(got, 1)
	s.Equal("a", got[0].Name)

	s.Len(Filter(cookies, []string{"example.com", "other.example"}), 2)
	s.Empty(Filter(cookies, []string{"unrelated.example"}))
	s.Nil(Filter(cookies, nil))
}
