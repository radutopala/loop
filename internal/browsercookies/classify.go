package browsercookies

import (
	_ "embed"
	"strings"
)

// Category labels how sensitive a site's cookies are. It drives the badge in
// the picker and, more importantly, whether the site is checked by default.
type Category string

const (
	// CategoryNone is an ordinary site: checked by default.
	CategoryNone Category = ""
	// CategoryEmail is a mailbox. Mail access is account recovery for
	// everything else, which is why it ranks with banks here.
	CategoryEmail Category = "email"
	// CategorySignin is an identity provider, SSO endpoint or password vault.
	CategorySignin Category = "signin"
	// CategoryBank is a bank, broker, card network or payment processor.
	CategoryBank Category = "bank"
	// CategorySensitive is a site the user marked sensitive in config.
	CategorySensitive Category = "sensitive"
)

//go:embed sensitive_domains.txt
var seedList string

// signinPrefixes are leftmost host labels that mean "this host exists to
// authenticate you". Matching on the label rather than the whole domain is
// what lets api-1a2b3c4d.duosecurity.com and oauth.officeapps.live.com
// classify without anybody having listed them.
var signinPrefixes = []string{
	"login", "signin", "sign-in", "sso", "oauth", "oauth2", "openid",
	"accounts", "account", "auth", "authn", "idp", "id", "identity", "mfa", "2fa",
}

// keywordCategories is the last resort, matched as a substring of the whole
// domain. It is deliberately loose: enablebanking.com should classify even
// though nobody will ever list it, and the cost of catching mailchimp.com
// along the way is one extra checkbox click.
var keywordCategories = []struct {
	keyword  string
	category Category
}{
	{"banking", CategoryBank},
	{"bank", CategoryBank},
	{"payment", CategoryBank},
	{"payments", CategoryBank},
	{"paypal", CategoryBank},
	{"wallet", CategoryBank},
	{"invoic", CategoryBank},
	{"mail", CategoryEmail},
	{"webmail", CategoryEmail},
	{"login", CategorySignin},
	{"signin", CategorySignin},
	{"passwor", CategorySignin},
}

// Classifier labels domains. The zero value is not usable; call
// NewClassifier.
type Classifier struct {
	seed  map[string]Category
	extra map[string]struct{}
}

// NewClassifier builds a classifier from the embedded seed list plus any
// domains the user marked sensitive in config.
func NewClassifier(userSensitive []string) *Classifier {
	c := &Classifier{
		seed:  parseSeedList(seedList),
		extra: make(map[string]struct{}, len(userSensitive)),
	}
	for _, d := range userSensitive {
		if d = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(d), ".")); d != "" {
			c.extra[d] = struct{}{}
		}
	}
	return c
}

// parseSeedList reads the embedded "<category> <domain>" lines. Blank lines
// and # comments are skipped.
func parseSeedList(text string) map[string]Category {
	out := make(map[string]Category)
	for line := range strings.Lines(text) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		category, domain, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		out[strings.TrimSpace(domain)] = Category(category)
	}
	return out
}

// Classify labels a domain, erring toward classifying.
//
// The asymmetry is deliberate: a false positive costs the user one extra
// click, while a false negative silently hands a bank or mailbox session to
// an agent. When a rule is unsure, it fires.
func (c *Classifier) Classify(domain string) Category {
	d := strings.ToLower(strings.TrimPrefix(domain, "."))
	if d == "" {
		return CategoryNone
	}

	if matchSuffix(c.extra, d) {
		return CategorySensitive
	}
	if cat := signinPrefixCategory(d); cat != CategoryNone {
		return cat
	}
	if cat := c.seedCategory(d); cat != CategoryNone {
		return cat
	}
	for _, k := range keywordCategories {
		if strings.Contains(d, k.keyword) {
			return k.category
		}
	}
	return CategoryNone
}

// signinPrefixCategory matches the leftmost label of a multi-label host.
// A bare "login.com" is left to the seed list and keywords; the rule here is
// about a host that authenticates for some parent domain.
func signinPrefixCategory(domain string) Category {
	label, rest, ok := strings.Cut(domain, ".")
	if !ok || !strings.Contains(rest, ".") {
		return CategoryNone
	}
	for _, p := range signinPrefixes {
		if label == p {
			return CategorySignin
		}
	}
	return CategoryNone
}

// seedCategory matches a domain or any of its parents against the seed list,
// so mail.google.com inherits google.com's label.
func (c *Classifier) seedCategory(domain string) Category {
	for _, d := range parentDomains(domain) {
		if cat, ok := c.seed[d]; ok {
			return cat
		}
	}
	return CategoryNone
}

// matchSuffix reports whether the domain, or any parent of it, is in the set.
func matchSuffix(set map[string]struct{}, domain string) bool {
	for _, d := range parentDomains(domain) {
		if _, ok := set[d]; ok {
			return true
		}
	}
	return false
}

// parentDomains yields the domain followed by each parent that still has two
// labels, so mail.google.com yields itself and google.com but never a bare
// "com" — a suffix rule that matched a TLD would classify half the web.
func parentDomains(domain string) []string {
	out := []string{domain}
	for d := domain; ; {
		_, rest, found := strings.Cut(d, ".")
		if !found || !strings.Contains(rest, ".") {
			return out
		}
		out = append(out, rest)
		d = rest
	}
}
