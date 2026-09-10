package browsercookies

import (
	"sort"
	"strings"
)

// DomainSummary is one row of the site picker.
type DomainSummary struct {
	Domain   string   `json:"domain"`
	Count    int      `json:"count"`
	Category Category `json:"category"`
}

// NormaliseDomain strips the leading dot Chromium and Firefox use to mark a
// cookie as valid for subdomains.
func NormaliseDomain(host string) string {
	return strings.ToLower(strings.TrimPrefix(host, "."))
}

// Summarise groups cookies into the rows the picker shows.
//
// Grouping is by the cookie's own scope — its host_key with the leading dot
// removed — not by registrable domain. That choice is what keeps the picker
// honest: a vendor's own scope and one of its customer tenants are two
// genuinely different grants, and folding the tenant into the vendor's
// registrable domain would let one checkbox hand over every tenant cookie in
// the profile. Ticking a row grants exactly the scope printed on it.
//
// Rows come back classified-first, then by cookie count descending, then
// alphabetically. Putting the risky ones where they cannot be scrolled past
// is the opposite of the usual instinct to bury them.
func Summarise(cookies []Cookie, classifier *Classifier) []DomainSummary {
	counts := make(map[string]int)
	for _, c := range cookies {
		counts[NormaliseDomain(c.Domain)]++
	}

	out := make([]DomainSummary, 0, len(counts))
	for domain, count := range counts {
		out = append(out, DomainSummary{
			Domain:   domain,
			Count:    count,
			Category: classifier.Classify(domain),
		})
	}

	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if (a.Category != CategoryNone) != (b.Category != CategoryNone) {
			return a.Category != CategoryNone
		}
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		return a.Domain < b.Domain
	})
	return out
}

// Filter returns the cookies whose scope is one of the selected domains.
//
// The match is exact against the normalised scope, so the user gets precisely
// the rows they ticked — no suffix rule quietly pulling in a subdomain that
// was listed separately and left unchecked.
func Filter(cookies []Cookie, domains []string) []Cookie {
	if len(domains) == 0 {
		return nil
	}
	wanted := make(map[string]struct{}, len(domains))
	for _, d := range domains {
		wanted[NormaliseDomain(d)] = struct{}{}
	}

	out := make([]Cookie, 0, len(cookies))
	for _, c := range cookies {
		if _, ok := wanted[NormaliseDomain(c.Domain)]; ok {
			out = append(out, c)
		}
	}
	return out
}
